// Code generated for DeepCopy and DeepCopyObject. Run "go generate ./api/..." to regenerate.

package v1alpha1

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyObject implements runtime.Object for DevEnvironment.
func (in *DevEnvironment) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(DevEnvironment)
	deepCopyJSON(in, out)
	return out
}

// DeepCopyObject implements runtime.Object for DevEnvironmentList.
func (in *DevEnvironmentList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(DevEnvironmentList)
	deepCopyJSON(in, out)
	return out
}

func deepCopyJSON(in, out interface{}) {
	data, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		panic(err)
	}
}
