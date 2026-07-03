// Package v1alpha1 defines the Tenant custom resource (ADR-0008).
// Tenant CRs are the control plane's source of truth: the reconcile loop
// converges the cluster to spec (namespace, Postgres, Zitadel virtual
// instance, app pods) and reports reality in status.
package v1alpha1

import (
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TenantSpec is the desired state. Slug is immutable after creation
// (ADR-0007): it forms the tenant domain, which is the OIDC issuer and
// must never change.
type TenantSpec struct {
	// Slug is a DNS label: the permanent, human-chosen tenant identifier.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="slug is immutable"
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`
	Slug string `json:"slug"`
	// DisplayName may change freely; it never appears in URLs or domains.
	DisplayName string `json:"displayName,omitempty"`
}

// TenantPhase summarizes where the reconcile loop stands.
type TenantPhase string

const (
	TenantPending  TenantPhase = "Pending"
	TenantReady    TenantPhase = "Ready"
	TenantDeleting TenantPhase = "Deleting"
	TenantFailed   TenantPhase = "Failed"
)

// TenantStatus is reported state, never edited by humans.
type TenantStatus struct {
	Phase TenantPhase `json:"phase,omitempty"`
	// Issuer is the tenant's OIDC issuer URL (its permanent domain).
	Issuer string `json:"issuer,omitempty"`
	// ClientID of the tenant's default PKCE app (ADR-0006 §6).
	ClientID   string             `json:"clientId,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Tenant is one isolated customer environment.
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantSpec   `json:"spec"`
	Status TenantStatus `json:"status,omitempty"`
}

// TenantList is what the control-plane UI renders.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tenant `json:"items"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidSlug reports whether s can be a tenant slug: a DNS label, because
// the slug becomes the leftmost label of the tenant domain.
func ValidSlug(s string) bool {
	return slugRe.MatchString(s)
}
