// +kubebuilder:object:generate=true
// +groupName=peristera.io

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion identifies the Tenant API served by this module.
	GroupVersion = schema.GroupVersion{Group: "peristera.io", Version: "v1alpha1"}

	// SchemeBuilder registers our types with a runtime scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme is what consumers (manager, clients) call.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&Tenant{}, &TenantList{})
}
