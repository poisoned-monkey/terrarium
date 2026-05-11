// Sync-client — watches paths from a DevEnvironment and uploads changes to the pod sidecar (kubectl port-forward + HTTP).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	devv1alpha1 "github.com/poisoned-monkey/terrarium/api/v1alpha1"
)

const syncReceiverPort = 9090

func main() {
	devenv := flag.String("devenv", "", "DevEnvironment name")
	service := flag.String("service", "", "Service name in the stack")
	baseDir := flag.String("base-dir", ".", "Base directory for sync paths")
	localPort := flag.Int("port", 19090, "Local port for port-forward")
	noReload := flag.Bool("no-reload", false, "Skip POST /reload after sync")
	flag.Parse()

	if *devenv == "" || *service == "" {
		log.Fatal("--devenv and --service are required")
	}

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = devv1alpha1.AddToScheme(scheme)

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("kube config: %v", err)
	}

	kclient, err := ctrl.New(cfg, ctrl.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	var env devv1alpha1.DevEnvironment
	if err := kclient.Get(context.Background(), ctrl.ObjectKey{Name: *devenv}, &env); err != nil {
		log.Fatalf("get DevEnvironment %s: %v", *devenv, err)
	}

	ns := env.Spec.Namespace
	if ns == "" {
		ns = env.Status.Namespace
	}
	if ns == "" {
		ns = "dev-" + strings.ReplaceAll(strings.ReplaceAll(*devenv, "/", "-"), "_", "-")
	}

	var syncSpec *devv1alpha1.SyncSpec
	for i := range env.Spec.Stack.Services {
		if env.Spec.Stack.Services[i].Name == *service {
			syncSpec = env.Spec.Stack.Services[i].Sync
			break
		}
	}
	if syncSpec == nil || len(syncSpec.Paths) == 0 {
		log.Fatalf("service %s not found or missing sync.paths", *service)
	}

	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}

	pods, err := k8s.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{
		LabelSelector: labels.Set{"app": *service}.String(),
	})
	if err != nil {
		log.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) == 0 {
		log.Fatalf("no pod found for service %s in namespace %s", *service, ns)
	}
	pod := pods.Items[0]
	if pod.Status.Phase != corev1.PodRunning {
		log.Fatalf("pod %s is not Running", pod.Name)
	}

	// Port-forward via kubectl
	portForward := exec.Command("kubectl", "port-forward", "-n", ns, "pod/"+pod.Name,
		fmt.Sprintf("%d:%d", *localPort, syncReceiverPort))
	portForward.Stdout = os.Stdout
	portForward.Stderr = os.Stderr
	if err := portForward.Start(); err != nil {
		log.Fatalf("port-forward start: %v", err)
	}
	defer portForward.Process.Kill()
	time.Sleep(1 * time.Second)

	base, err := filepath.Abs(*baseDir)
	if err != nil {
		log.Fatalf("base-dir: %v", err)
	}

	exclude := make(map[string]bool)
	for _, e := range syncSpec.Exclude {
		exclude[e] = true
	}

	uploadFile := func(relPath string) {
		localPath := filepath.Join(base, relPath)
		body, err := os.ReadFile(localPath)
		if err != nil {
			log.Printf("read %s: %v", localPath, err)
			return
		}
		urlPath := "/files/" + filepath.ToSlash(relPath)
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d%s", *localPort, urlPath), strings.NewReader(string(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("upload %s: %v", relPath, err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("upload %s: %d", relPath, resp.StatusCode)
			return
		}
		log.Printf("sync %s", relPath)
	}

	// Initial sync: upload all files under the configured paths
	for _, p := range syncSpec.Paths {
		abs := filepath.Join(base, p)
		filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(base, path)
			if shouldExclude(rel, exclude) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			uploadFile(rel)
			return nil
		})
	}
	if !*noReload && syncSpec.HotReload {
		resp, _ := http.Post(fmt.Sprintf("http://127.0.0.1:%d/reload", *localPort), "", nil)
		if resp != nil {
			resp.Body.Close()
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("watcher: %v", err)
	}
	defer watcher.Close()

	for _, p := range syncSpec.Paths {
		abs := filepath.Join(base, p)
		if err := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && !shouldExclude(path, exclude) {
				watcher.Add(path)
			}
			return nil
		}); err != nil {
			log.Printf("watch add %s: %v", abs, err)
		}
	}

	log.Printf("watching (base=%s), reload=%v", base, !*noReload)
	for {
		select {
		case ev := <-watcher.Events:
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			rel, err := filepath.Rel(base, ev.Name)
			if err != nil {
				continue
			}
			if shouldExclude(rel, exclude) {
				continue
			}
			if info, err := os.Stat(ev.Name); err != nil || info.IsDir() {
				continue
			}
			uploadFile(rel)
			if !*noReload && syncSpec.HotReload {
				time.Sleep(100 * time.Millisecond)
				http.Post(fmt.Sprintf("http://127.0.0.1:%d/reload", *localPort), "", nil)
			}
		case err := <-watcher.Errors:
			log.Printf("watcher error: %v", err)
		}
	}
}

func shouldExclude(path string, exclude map[string]bool) bool {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if exclude[p] {
			return true
		}
	}
	return false
}
