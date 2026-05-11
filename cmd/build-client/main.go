// Build-client — builds an image from spec.build and updates the Deployment in the cluster.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	devv1alpha1 "github.com/poisoned-monkey/terrarium/api/v1alpha1"
)

func main() {
	devenv := flag.String("devenv", "", "DevEnvironment name")
	service := flag.String("service", "", "Service name in the stack")
	baseDir := flag.String("base-dir", ".", "Base directory (build context is relative to this)")
	imageTag := flag.String("image", "", "Full image name (e.g. myreg.com/dev-api:v1). If empty, generated from REGISTRY.")
	noPush := flag.Bool("no-push", false, "Skip pushing the image to the registry (docker build only)")
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

	var buildSpec *devv1alpha1.BuildSpec
	for i := range env.Spec.Stack.Services {
		if env.Spec.Stack.Services[i].Name == *service {
			buildSpec = env.Spec.Stack.Services[i].Build
			break
		}
	}
	if buildSpec == nil || buildSpec.Context == "" {
		log.Fatalf("service %s not found or missing build.context", *service)
	}

	contextDir := filepath.Join(*baseDir, buildSpec.Context)
	contextDir, err = filepath.Abs(contextDir)
	if err != nil {
		log.Fatalf("context dir: %v", err)
	}
	if _, err := os.Stat(contextDir); err != nil {
		log.Fatalf("context dir %s: %v", contextDir, err)
	}

	dockerfile := buildSpec.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	image := *imageTag
	if image == "" {
		reg := os.Getenv("REGISTRY")
		if reg == "" {
			reg = "dev-environment"
		}
		reg = strings.TrimSuffix(reg, "/")
		safeName := strings.ReplaceAll(strings.ReplaceAll(*devenv, "/", "-"), "_", "-")
		image = fmt.Sprintf("%s/dev-%s-%s:latest", reg, safeName, *service)
	}

	// docker build
	args := []string{"build", "-t", image}
	if dockerfile != "" {
		args = append(args, "-f", filepath.Join(contextDir, dockerfile))
	}
	args = append(args, contextDir)
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("docker build: %v", err)
	}
	log.Printf("built %s", image)

	if !*noPush {
		push := exec.Command("docker", "push", image)
		push.Stdout = os.Stdout
		push.Stderr = os.Stderr
		if err := push.Run(); err != nil {
			log.Fatalf("docker push: %v", err)
		}
		log.Printf("pushed %s", image)
	}

	// Patch Deployment
	var dep appsv1.Deployment
	if err := kclient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: *service}, &dep); err != nil {
		log.Fatalf("get Deployment: %v", err)
	}
	if len(dep.Spec.Template.Spec.Containers) == 0 {
		log.Fatal("Deployment has no containers")
	}
	dep.Spec.Template.Spec.Containers[0].Image = image
	if err := kclient.Update(context.Background(), &dep); err != nil {
		log.Fatalf("update Deployment: %v", err)
	}
	log.Printf("Deployment %s/%s updated: image=%s", ns, *service, image)
}
