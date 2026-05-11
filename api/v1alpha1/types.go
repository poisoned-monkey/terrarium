// API types for the DevEnvironment CRD.
// To regenerate DeepCopy methods, install controller-gen and run:
//   go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
//   controller-gen object:headerFile=hack/boilerplate.go.txt paths=./api/...

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DevEnvironmentPhase represents the lifecycle phase of a dev environment.
type DevEnvironmentPhase string

const (
	DevEnvironmentPhasePending      DevEnvironmentPhase = "Pending"
	DevEnvironmentPhaseProvisioning DevEnvironmentPhase = "Provisioning"
	DevEnvironmentPhaseReady        DevEnvironmentPhase = "Ready"
	DevEnvironmentPhaseSyncing      DevEnvironmentPhase = "Syncing"
	DevEnvironmentPhaseFailed       DevEnvironmentPhase = "Failed"
	DevEnvironmentPhaseTerminating  DevEnvironmentPhase = "Terminating"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=devenv
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Branch",type=string,JSONPath=`.spec.branch`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DevEnvironment describes a single dev environment (e.g. for a feature branch).
type DevEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DevEnvironmentSpec   `json:"spec,omitempty"`
	Status DevEnvironmentStatus `json:"status,omitempty"`
}

// DevEnvironmentSpec is the desired state of a dev environment.
type DevEnvironmentSpec struct {
	Namespace string `json:"namespace,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Repo      string `json:"repo,omitempty"`

	Stack      DevStack        `json:"stack"`
	ConfigSync *ConfigSyncSpec `json:"configSync,omitempty"`
	RBAC       *RBACSpec       `json:"rbac,omitempty"`
	Secrets    []SecretRef     `json:"secrets,omitempty"`
	ConfigMaps []ConfigMapRef  `json:"configMaps,omitempty"`

	// TTLSecondsAfterIdle is the number of idle seconds before the CR is marked for deletion (0 = disabled).
	TTLSecondsAfterIdle *int64 `json:"ttlSecondsAfterIdle,omitempty"`
}

// DevStack is the declarative stack: services, databases, queues.
type DevStack struct {
	Services  []DevService  `json:"services"`
	Databases []DevDatabase `json:"databases,omitempty"`
	Queues    []DevQueue    `json:"queues,omitempty"`
}

// DevService is a single application service in the dev stack.
type DevService struct {
	Name     string     `json:"name"`
	Image    string     `json:"image,omitempty"`
	Build    *BuildSpec `json:"build,omitempty"`
	Sync     *SyncSpec  `json:"sync,omitempty"`
	Replicas *int32     `json:"replicas,omitempty"`
	Env      []EnvVar   `json:"env,omitempty"`
	Ports    []PortSpec `json:"ports,omitempty"`
}

// BuildSpec describes a local image build.
type BuildSpec struct {
	Context    string `json:"context,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
}

// SyncSpec describes live code sync and hot-reload.
type SyncSpec struct {
	Paths     []string `json:"paths,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
	HotReload bool     `json:"hotReload,omitempty"`
	Command   []string `json:"command,omitempty"`
}

// EnvVar is a container environment variable (simplified; can be extended with valueFrom).
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// PortSpec is a container port.
type PortSpec struct {
	ContainerPort int32  `json:"containerPort"`
	Name          string `json:"name,omitempty"`
}

// DevDatabase is a database in the dev stack.
type DevDatabase struct {
	Name    string       `json:"name"`
	Type    string       `json:"type"` // postgres, mysql, redis, mongodb
	Version string       `json:"version,omitempty"`
	Storage *StorageSpec `json:"storage,omitempty"`
}

// StorageSpec defines storage size (PVC).
type StorageSpec struct {
	Size string `json:"size,omitempty"`
}

// DevQueue is a message queue in the dev stack.
type DevQueue struct {
	Name string `json:"name"`
	Type string `json:"type"` // kafka, rabbitmq, nats
}

// ConfigSyncSpec describes config synchronization into the cluster.
type ConfigSyncSpec struct {
	Paths     []string `json:"paths,omitempty"`
	HotReload bool     `json:"hotReload,omitempty"`
}

// RBACSpec describes RBAC configuration for the dev namespace.
type RBACSpec struct {
	CreateDefaultRoleBinding bool          `json:"createDefaultRoleBinding,omitempty"`
	Subjects                 []RBACSubject `json:"subjects,omitempty"`
}

// RBACSubject is a subject for a RoleBinding.
type RBACSubject struct {
	Kind      string `json:"kind"`      // ServiceAccount, User, Group
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// SecretRef references an existing Secret or provides inline data.
type SecretRef struct {
	Name       string            `json:"name"`
	FromSecret *SecretSource     `json:"fromSecret,omitempty"`
	Literal    map[string]string `json:"literal,omitempty"`
}

// SecretSource references an existing Secret by name and namespace.
type SecretSource struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// ConfigMapRef references an existing ConfigMap or provides inline data.
type ConfigMapRef struct {
	Name          string            `json:"name"`
	FromConfigMap *ConfigMapSource  `json:"fromConfigMap,omitempty"`
	Literal       map[string]string `json:"literal,omitempty"`
}

// ConfigMapSource references an existing ConfigMap by name and namespace.
type ConfigMapSource struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// DevEnvironmentStatus is the observed state (populated by the operator).
type DevEnvironmentStatus struct {
	Phase              DevEnvironmentPhase `json:"phase,omitempty"`
	Namespace          string              `json:"namespace,omitempty"`
	Conditions         []metav1.Condition  `json:"conditions,omitempty"`
	LastSyncTime       *metav1.Time        `json:"lastSyncTime,omitempty"`
	ObservedGeneration int64               `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true

// DevEnvironmentList contains a list of DevEnvironment resources.
type DevEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DevEnvironment `json:"items"`
}
