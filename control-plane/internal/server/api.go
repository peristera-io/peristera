package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
	"github.com/peristera-io/peristera/control-plane/internal/server/gen"
	"github.com/peristera-io/peristera/control-plane/internal/zitadel"
)

// api implements gen.ServerInterface (generated from api/openapi.yaml).
type api struct{ s *Server }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, gen.Error{Message: msg})
}

func (a *api) toAPITenant(t *v1alpha1.Tenant) gen.Tenant {
	phase := gen.TenantPhase(t.Status.Phase)
	out := gen.Tenant{
		Slug:      t.Spec.Slug,
		Phase:     phase,
		Permalink: a.s.Cfg.PublicURL + "/api/v1/tenants/" + t.Spec.Slug,
	}
	if t.Spec.DisplayName != "" {
		out.DisplayName = &t.Spec.DisplayName
	}
	if t.Status.Issuer != "" {
		out.Issuer = &t.Status.Issuer
	}
	if t.Status.ClientID != "" {
		out.ClientId = &t.Status.ClientID
	}
	return out
}

func (a *api) ListTenants(w http.ResponseWriter, r *http.Request) {
	list := &v1alpha1.TenantList{}
	if err := a.s.K8s.List(r.Context(), list); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Spec.Slug < list.Items[j].Spec.Slug
	})
	out := make([]gen.Tenant, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, a.toAPITenant(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var in gen.TenantCreate
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !v1alpha1.ValidSlug(in.Slug) {
		apiError(w, http.StatusBadRequest, "slug must be a DNS label")
		return
	}
	t := &v1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: in.Slug},
		Spec:       v1alpha1.TenantSpec{Slug: in.Slug},
	}
	if in.DisplayName != nil {
		t.Spec.DisplayName = *in.DisplayName
	}
	if in.Domain != nil && *in.Domain != "" {
		if !v1alpha1.ValidDomain(*in.Domain) {
			apiError(w, http.StatusBadRequest, "domain must be a valid FQDN")
			return
		}
		t.Spec.Domain = *in.Domain
	}
	err := a.s.K8s.Create(r.Context(), t)
	switch {
	case apierrors.IsAlreadyExists(err):
		apiError(w, http.StatusConflict, "tenant exists")
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a.toAPITenant(t))
}

func (a *api) GetTenant(w http.ResponseWriter, r *http.Request, slug string) {
	t := &v1alpha1.Tenant{}
	err := a.s.K8s.Get(r.Context(), client.ObjectKey{Name: slug}, t)
	switch {
	case apierrors.IsNotFound(err):
		apiError(w, http.StatusNotFound, "no such tenant")
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.toAPITenant(t))
}

func (a *api) DeleteTenant(w http.ResponseWriter, r *http.Request, slug string) {
	t := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: slug}}
	err := a.s.K8s.Delete(r.Context(), t)
	switch {
	case apierrors.IsNotFound(err):
		apiError(w, http.StatusNotFound, "no such tenant")
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreateTenantUser creates an admin user in the tenant's own Zitadel instance
// and returns a generated one-time password — the operator's handover artifact
// (no more digging a Secret out with kubectl), and the same path for lost-login
// recovery. The password is returned once and never stored.
func (a *api) CreateTenantUser(w http.ResponseWriter, r *http.Request, slug string) {
	var in gen.TenantUserCreate
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	login, password, err := a.s.createTenantUser(r.Context(), slug, string(in.Email))
	switch {
	case errors.Is(err, errBadEmail):
		apiError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errNoTenant):
		apiError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errNotProvisioned):
		apiError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, zitadel.ErrUserExists):
		apiError(w, http.StatusConflict, "a user with this email already exists")
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusCreated, gen.TenantUserCredentials{Login: login, Password: password})
	}
}
