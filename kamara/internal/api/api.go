// Package api is Kamara's HTTP storage API (v0) — the surface other apps
// (Ergonomos) and, later, the browser UI call. It is a thin adapter over
// the file domain: authenticate the bearer token to a subject, authorize
// and act via file.Service, and shape JSON. The contract is api/openapi.yaml
// (OpenAPI-first, ADR-0007); handler operationIds match it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/lib/pii"
)

// Service is the file-domain surface the handlers use (satisfied by
// *file.Service; an interface so the handlers are testable with a fake).
type Service interface {
	Upload(ctx context.Context, owner pii.Subject, name string, r io.Reader) (file.Object, error)
	Download(ctx context.Context, caller pii.Subject, id string, w io.Writer) error
	List(ctx context.Context, caller pii.Subject) ([]file.Object, error)
	Get(ctx context.Context, caller pii.Subject, id string) (file.Object, error)
	Delete(ctx context.Context, caller pii.Subject, id string) error
}

// Authenticator resolves a bearer token to the caller's subject. The
// production impl (userinfoAuth) validates against the tenant OIDC
// provider's userinfo endpoint; ok is false for a missing/invalid token.
type Authenticator interface {
	Subject(ctx context.Context, token string) (subject pii.Subject, ok bool, err error)
}

// Handler serves the storage API.
type Handler struct {
	svc  Service
	auth Authenticator
}

// New builds the handler.
func New(svc Service, auth Authenticator) *Handler {
	return &Handler{svc: svc, auth: auth}
}

// Routes returns the mux for the /v1 surface. Mount it under "/v1/".
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/files", h.authed(h.list))
	mux.Handle("POST /v1/files", h.authed(h.upload))
	mux.Handle("GET /v1/files/{id}", h.authed(h.get))
	mux.Handle("DELETE /v1/files/{id}", h.authed(h.delete))
	mux.Handle("GET /v1/files/{id}/content", h.authed(h.download))
	return mux
}

// subjectHandler is a handler that has already resolved the caller.
type subjectHandler func(w http.ResponseWriter, r *http.Request, caller pii.Subject)

// authed wraps a handler with bearer authentication: it extracts and
// validates the token, resolves the subject, and passes it through. A
// missing or invalid token is a JSON 401.
func (h *Handler) authed(next subjectHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		caller, ok, err := h.auth.Subject(r.Context(), tok)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "authenticating: "+err.Error())
			return
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next(w, r, caller)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name query parameter is required")
		return
	}
	o, err := h.svc.Upload(r.Context(), caller, name, r.Body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toFile(o))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	objs, err := h.svc.List(r.Context(), caller)
	if err != nil {
		h.fail(w, err)
		return
	}
	files := make([]fileDTO, 0, len(objs))
	for _, o := range objs {
		files = append(files, toFile(o))
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	o, err := h.svc.Get(r.Context(), caller, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toFile(o))
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	// The metadata read first both authorizes and gives the name so the
	// stream carries a filename; then reassemble the bytes.
	o, err := h.svc.Get(r.Context(), caller, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+quoteName(o.Name))
	if err := h.svc.Download(r.Context(), caller, o.ID, w); err != nil {
		// Headers may already be flushed; a mid-stream error can only be
		// logged, not turned into a clean status. Reassembly failures are
		// integrity errors (a tampered/missing blob), rare in practice.
		h.fail(w, err)
		return
	}
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	if err := h.svc.Delete(r.Context(), caller, r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail maps a domain error to a status: not-found → 404, forbidden → 403,
// anything else → 500.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, file.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, file.ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden")
	default:
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

// fileDTO is the wire shape of components.schemas.File.
type fileDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Permalink string `json:"permalink"`
	Created   string `json:"created"`
	Updated   string `json:"updated"`
}

func toFile(o file.Object) fileDTO {
	return fileDTO{
		ID: o.ID, Name: o.Name, Size: o.Size, Permalink: o.Permalink(),
		Created: o.Created.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Updated: o.Updated.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// quoteName escapes a filename for a Content-Disposition header value.
func quoteName(name string) string {
	return `"` + strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(name) + `"`
}
