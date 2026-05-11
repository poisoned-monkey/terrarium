// DevEnvironment controller: creates namespace, RBAC, secrets, service deployments, and databases.
package controllers

import (
	"context"
	"os"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	devv1alpha1 "github.com/poisoned-monkey/terrarium/api/v1alpha1"
)

const (
	devEnvLabelKey           = "dev.example.com/devenvironment"
	syncWorkspaceVolumeName  = "sync-workspace"
	defaultSyncReceiverImage = "dev-environment-operator/sync-receiver:latest"
	syncReceiverPort         = 9090
)

// DevEnvironmentReconciler reconciles DevEnvironment resources.
type DevEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// NewDevEnvironmentReconciler creates a new reconciler.
func NewDevEnvironmentReconciler(mgr ctrl.Manager) *DevEnvironmentReconciler {
	return &DevEnvironmentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
}

// SetupWithManager registers the controller with the manager.
func (r *DevEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&devv1alpha1.DevEnvironment{}).
		Owns(&corev1.Namespace{}).
		Complete(r)
	// Other resources are created manually with an OwnerRef pointing to the DevEnvironment (cluster-scoped owner).
}

// Reconcile processes a single DevEnvironment.
func (r *DevEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env devv1alpha1.DevEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Determine the target namespace name.
	nsName := resolveNamespace(&env)
	if nsName == "" {
		nsName = "dev-" + sanitizeName(env.Name)
	}

	// On CR deletion the garbage collector removes the Namespace and all resources with an OwnerReference to this CR.

	// Update status (re-get to avoid resourceVersion conflict).
	var envFresh devv1alpha1.DevEnvironment
	if err := r.Get(ctx, req.NamespacedName, &envFresh); err != nil {
		return ctrl.Result{}, err
	}
	envFresh.Status.ObservedGeneration = env.Generation
	envFresh.Status.Namespace = nsName
	envFresh.Status.Phase = devv1alpha1.DevEnvironmentPhaseProvisioning
	if err := r.Status().Update(ctx, &envFresh); err != nil {
		return ctrl.Result{}, err
	}

	// 1) Namespace
	ns := namespaceFor(nsName, &env)
	if err := r.createOrUpdateNamespace(ctx, ns, &env); err != nil {
		return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
	}

	// 2) RBAC
	if env.Spec.RBAC != nil {
		if err := r.reconcileRBAC(ctx, nsName, &env); err != nil {
			return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
		}
	}

	// 3) Secrets
	for i := range env.Spec.Secrets {
		if err := r.reconcileSecret(ctx, nsName, &env.Spec.Secrets[i], &env); err != nil {
			return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
		}
	}

	// 4) ConfigMaps
	for i := range env.Spec.ConfigMaps {
		if err := r.reconcileConfigMap(ctx, nsName, &env.Spec.ConfigMaps[i], &env); err != nil {
			return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
		}
	}

	// 5) Services (Deployments + Services)
	for i := range env.Spec.Stack.Services {
		if err := r.reconcileService(ctx, nsName, &env.Spec.Stack.Services[i], &env); err != nil {
			return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
		}
	}

	// 6) Databases (simplified: Deployment + Service)
	for i := range env.Spec.Stack.Databases {
		if err := r.reconcileDatabase(ctx, nsName, &env.Spec.Stack.Databases[i], &env); err != nil {
			return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
		}
	}

	// 7) Queues (simplified: Deployment + Service)
	for i := range env.Spec.Stack.Queues {
		if err := r.reconcileQueue(ctx, nsName, &env.Spec.Stack.Queues[i], &env); err != nil {
			return r.setPhaseAndRequeue(ctx, &env, devv1alpha1.DevEnvironmentPhaseFailed, err)
		}
	}

	// Final status update — re-get for the current resourceVersion.
	if err := r.Get(ctx, req.NamespacedName, &envFresh); err != nil {
		return ctrl.Result{}, err
	}
	envFresh.Status.Phase = devv1alpha1.DevEnvironmentPhaseReady
	envFresh.Status.ObservedGeneration = env.Generation
	if err := r.Status().Update(ctx, &envFresh); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("reconcile ok", "namespace", nsName)
	return ctrl.Result{}, nil
}

func resolveNamespace(env *devv1alpha1.DevEnvironment) string {
	s := strings.TrimSpace(env.Spec.Namespace)
	if s != "" {
		return s
	}
	if env.Spec.Branch != "" {
		return "dev-" + sanitizeName(env.Spec.Branch)
	}
	return ""
}

var sanitizeRe = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = sanitizeRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func namespaceFor(name string, env *devv1alpha1.DevEnvironment) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	ns.Labels = map[string]string{
		devEnvLabelKey: env.Name,
	}
	return ns
}

func (r *DevEnvironmentReconciler) createOrUpdateNamespace(ctx context.Context, ns *corev1.Namespace, env *devv1alpha1.DevEnvironment) error {
	if err := controllerutil.SetControllerReference(env, ns, r.Scheme); err != nil {
		return err
	}
	existing := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: ns.Name}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, ns)
		}
		return err
	}
	existing.Labels = ns.Labels
	if err := controllerutil.SetControllerReference(env, existing, r.Scheme); err != nil {
		return err
	}
	return r.Update(ctx, existing)
}

func (r *DevEnvironmentReconciler) setPhaseAndRequeue(ctx context.Context, env *devv1alpha1.DevEnvironment, phase devv1alpha1.DevEnvironmentPhase, err error) (ctrl.Result, error) {
	var fresh devv1alpha1.DevEnvironment
	if getErr := r.Get(ctx, client.ObjectKeyFromObject(env), &fresh); getErr == nil {
		fresh.Status.Phase = phase
		fresh.Status.ObservedGeneration = env.Generation
		_ = r.Status().Update(ctx, &fresh)
	}
	return ctrl.Result{}, err
}

// reconcileRBAC creates a RoleBinding in the namespace (admin ClusterRole for the specified subjects).
func (r *DevEnvironmentReconciler) reconcileRBAC(ctx context.Context, nsName string, env *devv1alpha1.DevEnvironment) error {
	rbac := env.Spec.RBAC
	if rbac == nil || !rbac.CreateDefaultRoleBinding && len(rbac.Subjects) == 0 {
		return nil
	}

	subjects := make([]rbacv1.Subject, 0, len(rbac.Subjects))
	for _, s := range rbac.Subjects {
		subjects = append(subjects, rbacv1.Subject{
			Kind:      s.Kind,
			Name:      s.Name,
			Namespace: s.Namespace,
		})
	}
	if rbac.CreateDefaultRoleBinding && len(subjects) == 0 {
		// createDefaultRoleBinding is true but no subjects provided — nothing to bind.
		return nil
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dev-environment-developers",
			Namespace: nsName,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "admin",
		},
		Subjects: subjects,
	}
	if err := controllerutil.SetControllerReference(env, roleBinding, r.Scheme); err != nil {
		return err
	}

	existing := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{Name: roleBinding.Name, Namespace: nsName}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, roleBinding)
		}
		return err
	}
	existing.Subjects = roleBinding.Subjects
	return r.Update(ctx, existing)
}

func (r *DevEnvironmentReconciler) reconcileSecret(ctx context.Context, nsName string, ref *devv1alpha1.SecretRef, env *devv1alpha1.DevEnvironment) error {
	if ref.FromSecret != nil {
		src := &corev1.Secret{}
		srcNs := ref.FromSecret.Namespace
		if srcNs == "" {
			srcNs = "default"
		}
		if err := r.Get(ctx, types.NamespacedName{Namespace: srcNs, Name: ref.FromSecret.Name}, src); err != nil {
			if errors.IsNotFound(err) {
				log.FromContext(ctx).Info("secret not found, skipping", "secret", ref.FromSecret.Name, "namespace", srcNs)
				return nil
			}
			return err
		}
		copy := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: nsName},
			Data:       src.Data,
			Type:       src.Type,
		}
		if err := controllerutil.SetControllerReference(env, copy, r.Scheme); err != nil {
			return err
		}
		return r.createOrUpdateSecret(ctx, copy)
	}
	if len(ref.Literal) != 0 {
		data := make(map[string][]byte)
		for k, v := range ref.Literal {
			data[k] = []byte(v)
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: nsName},
			Data:       data,
		}
		if err := controllerutil.SetControllerReference(env, secret, r.Scheme); err != nil {
			return err
		}
		return r.createOrUpdateSecret(ctx, secret)
	}
	return nil
}

func (r *DevEnvironmentReconciler) reconcileConfigMap(ctx context.Context, nsName string, ref *devv1alpha1.ConfigMapRef, env *devv1alpha1.DevEnvironment) error {
	if ref.FromConfigMap != nil {
		src := &corev1.ConfigMap{}
		srcNs := ref.FromConfigMap.Namespace
		if srcNs == "" {
			srcNs = "default"
		}
		if err := r.Get(ctx, types.NamespacedName{Namespace: srcNs, Name: ref.FromConfigMap.Name}, src); err != nil {
			if errors.IsNotFound(err) {
				log.FromContext(ctx).Info("configmap not found, skipping", "configmap", ref.FromConfigMap.Name, "namespace", srcNs)
				return nil
			}
			return err
		}
		copy := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: nsName},
			Data:       src.Data,
			BinaryData: src.BinaryData,
		}
		if err := controllerutil.SetControllerReference(env, copy, r.Scheme); err != nil {
			return err
		}
		return r.createOrUpdateConfigMap(ctx, copy)
	}
	if len(ref.Literal) != 0 {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: nsName},
			Data:       ref.Literal,
		}
		if err := controllerutil.SetControllerReference(env, cm, r.Scheme); err != nil {
			return err
		}
		return r.createOrUpdateConfigMap(ctx, cm)
	}
	return nil
}

func (r *DevEnvironmentReconciler) createOrUpdateSecret(ctx context.Context, secret *corev1.Secret) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKeyFromObject(secret), existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, secret)
		}
		return err
	}
	existing.Data = secret.Data
	existing.Type = secret.Type
	return r.Update(ctx, existing)
}

func (r *DevEnvironmentReconciler) createOrUpdateConfigMap(ctx context.Context, cm *corev1.ConfigMap) error {
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKeyFromObject(cm), existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, cm)
		}
		return err
	}
	existing.Data = cm.Data
	existing.BinaryData = cm.BinaryData
	return r.Update(ctx, existing)
}

func (r *DevEnvironmentReconciler) reconcileService(ctx context.Context, nsName string, svc *devv1alpha1.DevService, env *devv1alpha1.DevEnvironment) error {
	image := svc.Image
	if image == "" && svc.Build != nil {
		image = "busybox:1.36" // placeholder until the first build (build-client will update the image)
	}
	if image == "" {
		image = "busybox:1.36"
	}

	replicas := int32(1)
	if svc.Replicas != nil {
		replicas = *svc.Replicas
	}

	envVars := make([]corev1.EnvVar, 0, len(svc.Env))
	for _, e := range svc.Env {
		envVars = append(envVars, corev1.EnvVar{Name: e.Name, Value: e.Value})
	}

	containers := []corev1.Container{{
		Name:  svc.Name,
		Image: image,
		Env:   envVars,
	}}
	for _, p := range svc.Ports {
		containers[0].Ports = append(containers[0].Ports, corev1.ContainerPort{
			ContainerPort: p.ContainerPort,
			Name:          p.Name,
		})
	}

	var volumes []corev1.Volume
	if svc.Sync != nil {
		volumes = append(volumes, corev1.Volume{
			Name: syncWorkspaceVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
		containers[0].VolumeMounts = append(containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      syncWorkspaceVolumeName,
			MountPath: "/workspace",
		})
		reloadCmd := "touch /workspace/.reload"
		if len(svc.Sync.Command) > 0 {
			reloadCmd = strings.Join(svc.Sync.Command, " ")
		}
		syncImage := defaultSyncReceiverImage
		if envImg := os.Getenv("SYNC_RECEIVER_IMAGE"); envImg != "" {
			syncImage = envImg
		}
		containers = append(containers, corev1.Container{
			Name:  "sync-receiver",
			Image: syncImage,
			Env: []corev1.EnvVar{
				{Name: "WORKSPACE", Value: "/workspace"},
				{Name: "RELOAD_COMMAND", Value: reloadCmd},
				{Name: "PORT", Value: "9090"},
			},
			Ports: []corev1.ContainerPort{{ContainerPort: syncReceiverPort, Name: "sync"}},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      syncWorkspaceVolumeName,
				MountPath: "/workspace",
			}},
		})
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: svc.Name, Namespace: nsName},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": svc.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": svc.Name}},
				Spec: corev1.PodSpec{
					Containers: containers,
					Volumes:    volumes,
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(env, dep, r.Scheme); err != nil {
		return err
	}

	existingDep := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: dep.Name, Namespace: nsName}, existingDep)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, dep); err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		existingDep.Spec = dep.Spec
		if err := r.Update(ctx, existingDep); err != nil {
			return err
		}
	}

	// Service for in-cluster DNS access by name.
	if len(svc.Ports) > 0 {
		ports := make([]corev1.ServicePort, 0, len(svc.Ports))
		for _, p := range svc.Ports {
			ports = append(ports, corev1.ServicePort{
				Name:       p.Name,
				Port:       p.ContainerPort,
				TargetPort: intOrStr(p.ContainerPort),
			})
		}
		svcRes := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: svc.Name, Namespace: nsName},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": svc.Name},
				Ports:    ports,
			},
		}
		if err := controllerutil.SetControllerReference(env, svcRes, r.Scheme); err != nil {
			return err
		}
		existingSvc := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: svc.Name, Namespace: nsName}, existingSvc); err != nil {
			if errors.IsNotFound(err) {
				return r.Create(ctx, svcRes)
			}
			return err
		}
		existingSvc.Spec.Ports = svcRes.Spec.Ports
		existingSvc.Spec.Selector = svcRes.Spec.Selector
		return r.Update(ctx, existingSvc)
	}
	return nil
}

func intOrStr(p int32) intstr.IntOrString { return intstr.FromInt32(p) }

func (r *DevEnvironmentReconciler) reconcileDatabase(ctx context.Context, nsName string, db *devv1alpha1.DevDatabase, env *devv1alpha1.DevEnvironment) error {
	image, port := imageAndPortForDB(db.Type, db.Version)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: nsName},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": db.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": db.Name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  db.Name,
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: port, Name: "client"}},
					}},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(env, dep, r.Scheme); err != nil {
		return err
	}
	if err := r.createOrUpdateDeployment(ctx, dep); err != nil {
		return err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: nsName},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": db.Name},
			Ports:    []corev1.ServicePort{{Name: "client", Port: port, TargetPort: intOrStr(port)}},
		},
	}
	if err := controllerutil.SetControllerReference(env, svc, r.Scheme); err != nil {
		return err
	}
	return r.createOrUpdateService(ctx, svc)
}

func imageAndPortForDB(typ, version string) (image string, port int32) {
	switch typ {
	case "postgres":
		if version == "" {
			version = "15"
		}
		return "postgres:" + version, 5432
	case "redis":
		if version == "" {
			version = "7"
		}
		return "redis:" + version, 6379
	case "mysql":
		if version == "" {
			version = "8"
		}
		return "mysql:" + version, 3306
	case "mongodb":
		if version == "" {
			version = "7"
		}
		return "mongo:" + version, 27017
	default:
		return "busybox:1.36", 8080
	}
}

func (r *DevEnvironmentReconciler) reconcileQueue(ctx context.Context, nsName string, q *devv1alpha1.DevQueue, env *devv1alpha1.DevEnvironment) error {
	image, port := imageAndPortForQueue(q.Type)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: q.Name, Namespace: nsName},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": q.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": q.Name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  q.Name,
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: port, Name: "client"}},
					}},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(env, dep, r.Scheme); err != nil {
		return err
	}
	if err := r.createOrUpdateDeployment(ctx, dep); err != nil {
		return err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: q.Name, Namespace: nsName},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": q.Name},
			Ports:    []corev1.ServicePort{{Name: "client", Port: port, TargetPort: intOrStr(port)}},
		},
	}
	if err := controllerutil.SetControllerReference(env, svc, r.Scheme); err != nil {
		return err
	}
	return r.createOrUpdateService(ctx, svc)
}

func imageAndPortForQueue(typ string) (image string, port int32) {
	switch typ {
	case "rabbitmq":
		return "rabbitmq:3-management", 5672
	case "kafka":
		return "apache/kafka:3.6", 9092
	case "nats":
		return "nats:2.10", 4222
	default:
		return "busybox:1.36", 8080
	}
}

func (r *DevEnvironmentReconciler) createOrUpdateDeployment(ctx context.Context, dep *appsv1.Deployment) error {
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, dep)
		}
		return err
	}
	existing.Spec = dep.Spec
	return r.Update(ctx, existing)
}

func (r *DevEnvironmentReconciler) createOrUpdateService(ctx context.Context, svc *corev1.Service) error {
	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, svc)
		}
		return err
	}
	existing.Spec.Ports = svc.Spec.Ports
	existing.Spec.Selector = svc.Spec.Selector
	return r.Update(ctx, existing)
}

func ptr[T any](v T) *T { return &v }
