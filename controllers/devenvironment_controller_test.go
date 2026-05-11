package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	devv1alpha1 "github.com/poisoned-monkey/terrarium/api/v1alpha1"
)

func init() {
	_ = devv1alpha1.AddToScheme(scheme.Scheme)
}

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = devv1alpha1.AddToScheme(s)
	return s
}

func TestReconcile_CreatesNamespaceAndDeployment(t *testing.T) {
	env := &devv1alpha1.DevEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
		Spec: devv1alpha1.DevEnvironmentSpec{
			Namespace: "dev-test",
			Stack: devv1alpha1.DevStack{
				Services: []devv1alpha1.DevService{{
					Name:  "web",
					Image: "nginx:alpine",
					Ports: []devv1alpha1.PortSpec{{ContainerPort: 80, Name: "http"}},
				}},
			},
		},
	}

	scheme := newTestScheme()
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(env).
		WithStatusSubresource(env).
		Build()

	reconciler := &DevEnvironmentReconciler{
		Client: client,
		Scheme: scheme,
	}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: ctrl.ObjectKeyFromObject(env),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Namespace created
	var ns corev1.Namespace
	if err := client.Get(context.Background(), ctrl.ObjectKey{Name: "dev-test"}, &ns); err != nil {
		t.Errorf("namespace dev-test not created: %v", err)
	}

	// Deployment created
	var dep appsv1.Deployment
	if err := client.Get(context.Background(), ctrl.ObjectKey{Namespace: "dev-test", Name: "web"}, &dep); err != nil {
		t.Errorf("deployment web not created: %v", err)
	}
	if len(dep.Spec.Template.Spec.Containers) == 0 || dep.Spec.Template.Spec.Containers[0].Image != "nginx:alpine" {
		t.Errorf("expected image nginx:alpine, got %+v", dep.Spec.Template.Spec.Containers)
	}
}

func TestReconcile_AddsSyncSidecarWhenSyncSpecPresent(t *testing.T) {
	env := &devv1alpha1.DevEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "sync-test"},
		Spec: devv1alpha1.DevEnvironmentSpec{
			Namespace: "dev-sync-test",
			Stack: devv1alpha1.DevStack{
				Services: []devv1alpha1.DevService{{
					Name:  "api",
					Image: "busybox:1.36",
					Sync: &devv1alpha1.SyncSpec{
						Paths:     []string{"."},
						HotReload: true,
					},
				}},
			},
		},
	}

	scheme := newTestScheme()
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(env).
		WithStatusSubresource(env).
		Build()

	reconciler := &DevEnvironmentReconciler{
		Client: client,
		Scheme: scheme,
	}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: ctrl.ObjectKeyFromObject(env),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var dep appsv1.Deployment
	if err := client.Get(context.Background(), ctrl.ObjectKey{Namespace: "dev-sync-test", Name: "api"}, &dep); err != nil {
		t.Fatalf("deployment not created: %v", err)
	}
	if len(dep.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("expected 2 containers (app + sync-receiver), got %d", len(dep.Spec.Template.Spec.Containers))
	}
	var sidecarFound bool
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == "sync-receiver" {
			sidecarFound = true
			break
		}
	}
	if !sidecarFound {
		t.Error("sync-receiver container not found")
	}
}
