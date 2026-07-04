package server

import (
	"html/template"
	"net/http"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// messages is the string catalog — no hardcoded strings in templates
// (README §4: multilingual from the first template). EN only for now;
// FR/DE/LB are the target locales.
var messages = map[string]string{
	"title":          "Peristera Control Plane",
	"tenants":        "Tenants",
	"slug":           "Slug",
	"display_name":   "Display name",
	"phase":          "Phase",
	"issuer":         "Issuer",
	"actions":        "Actions",
	"create":         "Create tenant",
	"delete":         "Delete",
	"confirm_delete": "Delete this tenant and all its data?",
	"logged_in_as":   "Logged in as",
	"logout":         "Log out",
	"no_tenants":     "No tenants yet — create the first one below.",
}

var funcs = template.FuncMap{
	"msg": func(key string) string {
		if v, ok := messages[key]; ok {
			return v
		}
		return "⟪" + key + "⟫"
	},
}

var pageTmpl = template.Must(template.New("page").Funcs(funcs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{msg "title"}}</title>
  <script src="https://unpkg.com/htmx.org@2.0.4"></script>
  <style>
    body { font-family: system-ui, sans-serif; margin: 2rem auto; max-width: 60rem; }
    table { border-collapse: collapse; width: 100%; }
    th, td { text-align: left; padding: .5rem .75rem; border-bottom: 1px solid #ddd; }
    form.create { margin-top: 1.5rem; display: flex; gap: .5rem; }
    header { display: flex; justify-content: space-between; align-items: baseline; }
  </style>
</head>
<body>
<header>
  <h1>{{msg "title"}}</h1>
  <p>{{msg "logged_in_as"}} <strong>{{.User.Name}}</strong>
     — <a href="/auth/logout">{{msg "logout"}}</a></p>
</header>
<main>
  <h2>{{msg "tenants"}}</h2>
  {{if not .Tenants}}<p>{{msg "no_tenants"}}</p>{{end}}
  <table>
    <thead><tr>
      <th>{{msg "slug"}}</th><th>{{msg "display_name"}}</th>
      <th>{{msg "phase"}}</th><th>{{msg "issuer"}}</th><th>{{msg "actions"}}</th>
    </tr></thead>
    <tbody>
      {{range .Tenants}}{{template "row" .}}{{end}}
    </tbody>
  </table>
  <form class="create" hx-post="/ui/tenants" hx-swap="none">
    <input name="slug" placeholder="{{msg "slug"}}" required
           pattern="[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?">
    <input name="displayName" placeholder="{{msg "display_name"}}">
    <button type="submit">{{msg "create"}}</button>
  </form>
</main>
</body>
</html>
{{define "row"}}
<tr id="tenant-{{.Slug}}"{{if ne .Phase "Ready"}} hx-get="/ui/tenants/{{.Slug}}/row" hx-trigger="every 3s" hx-swap="outerHTML"{{end}}>
  <td>{{.Slug}}</td>
  <td>{{.DisplayName}}</td>
  <td>{{.Phase}}</td>
  <td>{{if .Issuer}}<a href="{{.Issuer}}">{{.Issuer}}</a>{{end}}</td>
  <td><button hx-delete="/ui/tenants/{{.Slug}}" hx-swap="none"
        hx-confirm="{{msg "confirm_delete"}}">{{msg "delete"}}</button></td>
</tr>
{{end}}`))

type rowData struct {
	Slug, DisplayName, Phase, Issuer string
}

func toRow(t *v1alpha1.Tenant) rowData {
	return rowData{
		Slug: t.Spec.Slug, DisplayName: t.Spec.DisplayName,
		Phase: string(t.Status.Phase), Issuer: t.Status.Issuer,
	}
}

func (s *Server) uiMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.uiIndex)
	mux.HandleFunc("POST /ui/tenants", s.uiCreate)
	mux.HandleFunc("DELETE /ui/tenants/{slug}", s.uiDelete)
	mux.HandleFunc("GET /ui/tenants/{slug}/row", s.uiRow)
	return mux
}

func (s *Server) uiIndex(w http.ResponseWriter, r *http.Request) {
	list := &v1alpha1.TenantList{}
	if err := s.K8s.List(r.Context(), list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Spec.Slug < list.Items[j].Spec.Slug
	})
	rows := make([]rowData, 0, len(list.Items))
	for i := range list.Items {
		rows = append(rows, toRow(&list.Items[i]))
	}
	user, _ := s.user(r)
	_ = pageTmpl.Execute(w, map[string]any{"User": user.Claims, "Tenants": rows})
}

func (s *Server) uiCreate(w http.ResponseWriter, r *http.Request) {
	slug := r.FormValue("slug")
	if !v1alpha1.ValidSlug(slug) {
		http.Error(w, messages["slug"], http.StatusBadRequest)
		return
	}
	t := &v1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: slug},
		Spec:       v1alpha1.TenantSpec{Slug: slug, DisplayName: r.FormValue("displayName")},
	}
	if err := s.K8s.Create(r.Context(), t); err != nil && !apierrors.IsAlreadyExists(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uiDelete(w http.ResponseWriter, r *http.Request) {
	t := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: r.PathValue("slug")}}
	if err := s.K8s.Delete(r.Context(), t); err != nil && !apierrors.IsNotFound(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uiRow(w http.ResponseWriter, r *http.Request) {
	t := &v1alpha1.Tenant{}
	if err := s.K8s.Get(r.Context(), client.ObjectKey{Name: r.PathValue("slug")}, t); err != nil {
		// Row vanished (deleted) — swap in nothing.
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = pageTmpl.ExecuteTemplate(w, "row", toRow(t))
}
