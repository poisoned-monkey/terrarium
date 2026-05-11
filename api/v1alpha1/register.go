package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// SchemeGroupVersion is the group version used to register these types.
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}
	// SchemeBuilder adds types to a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}
	// AddToScheme adds DevEnvironment and DevEnvironmentList to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&DevEnvironment{}, &DevEnvironmentList{})
}
