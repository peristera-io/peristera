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
	// "iam" and "cp" are the platform's own hosts under the base domain
	// and therefore reserved.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="slug is immutable"
	// +kubebuilder:validation:XValidation:rule="!(self in ['iam', 'cp'])",message="slug is reserved"
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`
	Slug string `json:"slug"`
	// DisplayName may change freely; it never appears in URLs or domains.
	DisplayName string `json:"displayName,omitempty"`
	// Domain, when set, is the tenant's custom apex (BYO domain, e.g.
	// "peristera.lu"): the public base for its OIDC issuer and app hosts
	// (<app>.<domain>) instead of the default <slug>.<platform-base-domain>.
	// Immutable, because it is the issuer. The domain must resolve to the
	// platform (delegated to its DNS, or its records pointed at the node) so
	// per-host certs can be issued. Empty = the default <slug>.<base> host.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="domain is immutable"
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`
	Domain string `json:"domain,omitempty"`
	// Apps is the set of optional catalog apps this tenant has enabled
	// (ADR-0013 optional-per-tenant dimension, ADR-0018). Always-on apps are
	// provisioned regardless; an optional app (e.g. "office", the Collabora
	// engine) is provisioned only when named here, since it carries a real
	// hardware cost. Editing the list enables or disables the feature.
	Apps []string `json:"apps,omitempty"`
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
	// InstanceID of the tenant's Zitadel virtual instance — the
	// idempotency record for IAM provisioning and the handle for
	// off-boarding (ADR-0006).
	InstanceID string `json:"instanceId,omitempty"`
	// Issuer is the tenant's OIDC issuer URL (its permanent domain).
	Issuer string `json:"issuer,omitempty"`
	// ClientID of the tenant's default PKCE app (ADR-0006 §6).
	ClientID   string             `json:"clientId,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Tenant is one isolated customer environment.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=tn
// +kubebuilder:printcolumn:name="Slug",type=string,JSONPath=`.spec.slug`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Issuer",type=string,JSONPath=`.status.issuer`
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantSpec   `json:"spec"`
	Status TenantStatus `json:"status,omitempty"`
}

// TenantList is what the control-plane UI renders.
// +kubebuilder:object:root=true
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

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)

// ValidDomain reports whether s is a usable custom tenant apex: a
// dotted, lowercase FQDN (e.g. "peristera.lu"). It becomes the tenant's
// public base domain and OIDC issuer.
func ValidDomain(s string) bool {
	return domainRe.MatchString(s)
}
