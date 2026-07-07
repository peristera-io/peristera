// WOPI host (ADR-0018): the three endpoints the office engine (Collabora)
// drives to open and save a file. It is a machine surface (no cookie, no CSRF)
// authenticated by the per-session WOPI access token Kamara minted, which is
// re-checked against OpenFGA on every call. Mounted under "/wopi/".
package api

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/wopi"
	"github.com/peristera-io/peristera/lib/pii"
)

// WopiService is the file-domain subset the WOPI host needs.
type WopiService interface {
	Get(ctx context.Context, caller pii.Subject, id string) (file.Object, error)
	Download(ctx context.Context, caller pii.Subject, id string, w io.Writer) error
	WriteVersion(ctx context.Context, caller pii.Subject, id string, r io.Reader) (version string, err error)
}

// Validator resolves a WOPI access token to its session (satisfied by
// *wopi.Sessions).
type Validator interface {
	Validate(ctx context.Context, token string) (wopi.Session, error)
}

// WopiHandler serves the WOPI host endpoints.
type WopiHandler struct {
	svc       WopiService
	sessions  Validator
	maxUpload int64
}

// NewWopi builds the WOPI host. maxUpload caps a PutFile body; <= 0 uses
// DefaultMaxUploadBytes.
func NewWopi(svc WopiService, sessions Validator, maxUpload int64) *WopiHandler {
	if maxUpload <= 0 {
		maxUpload = DefaultMaxUploadBytes
	}
	return &WopiHandler{svc: svc, sessions: sessions, maxUpload: maxUpload}
}

// Routes returns the mux for the WOPI surface. Mount it under "/wopi/".
func (h *WopiHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /wopi/files/{id}", h.session(h.checkFileInfo))
	mux.Handle("GET /wopi/files/{id}/contents", h.session(h.getFile))
	mux.Handle("POST /wopi/files/{id}/contents", h.session(h.putFile))
	return mux
}

// wopiHandler is a handler that has already resolved and scoped the session.
type wopiHandler func(w http.ResponseWriter, r *http.Request, s wopi.Session)

// session authenticates the WOPI access token, enforces that it is scoped to
// the file in the path, and passes the session through. Anything wrong is a
// bare 401 (WOPI clients key off the status, not a body).
func (h *WopiHandler) session(next wopiHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := h.sessions.Validate(r.Context(), wopiToken(r))
		if err != nil {
			if !errors.Is(err, wopi.ErrInvalid) {
				log.Printf("kamara: wopi validate: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// The token is bound to one file; it must match the path.
		if s.ObjectID != r.PathValue("id") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r, s)
	})
}

// wopiToken reads the access token from the Authorization: Bearer header
// (what coolwsd sends) or the access_token query parameter (the WOPI spec's
// transport) — accept both.
func wopiToken(r *http.Request) string {
	if t := bearer(r); t != "" {
		return t
	}
	return r.URL.Query().Get("access_token")
}

// checkFileInfo returns the WOPI CheckFileInfo metadata. Version changes on
// every save (it tracks updated_at), which is how the engine detects an
// external change.
func (h *WopiHandler) checkFileInfo(w http.ResponseWriter, r *http.Request, s wopi.Session) {
	log.Printf("kamara wopi: CheckFileInfo file=%s write=%v", s.ObjectID, s.CanWrite)
	o, err := h.svc.Get(r.Context(), s.Subject, s.ObjectID)
	if err != nil {
		h.failWopi(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"BaseFileName":     o.Name, // the extension tells the engine which editor to use
		"Size":             o.Size,
		"Version":          o.Updated.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		"OwnerId":          o.Owner.UserID,
		"UserId":           s.Subject.UserID,
		"UserFriendlyName": "Peristera user",
		"UserCanWrite":     s.CanWrite,
		"SupportsUpdate":   true,
		"SupportsLocks":    false, // single-user DoD; no lock endpoints (ADR-0018)
		"LastModifiedTime": o.Updated.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// getFile streams the file's decrypted bytes to the engine, with the stored
// content type (#28; falls back to octet-stream).
func (h *WopiHandler) getFile(w http.ResponseWriter, r *http.Request, s wopi.Session) {
	o, err := h.svc.Get(r.Context(), s.Subject, s.ObjectID)
	if err != nil {
		h.failWopi(w, err)
		return
	}
	log.Printf("kamara wopi: GetFile file=%s size=%d type=%s", s.ObjectID, o.Size, o.ContentType)
	w.Header().Set("Content-Type", ContentType(o.ContentType))
	if err := h.svc.Download(r.Context(), s.Subject, s.ObjectID, w); err != nil {
		// Status/bytes may be partly flushed; can only log (like the API download).
		log.Printf("kamara: wopi getfile %s: %v", s.ObjectID, err)
	}
}

// putFile saves the engine's new bytes as a new version (ADR-0018). It handles
// only the PUT override (no locks); the file keeps its owner, the acting user
// is recorded, and the new version ordinal is echoed in X-WOPI-ItemVersion.
func (h *WopiHandler) putFile(w http.ResponseWriter, r *http.Request, s wopi.Session) {
	ov := r.Header.Get("X-WOPI-Override")
	log.Printf("kamara wopi: PutFile file=%s override=%q write=%v", s.ObjectID, ov, s.CanWrite)
	if ov != "" && ov != "PUT" {
		// Lock/GetLock/etc. — unsupported (SupportsLocks=false).
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if !s.CanWrite {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	body := http.MaxBytesReader(w, r.Body, h.maxUpload)
	version, err := h.svc.WriteVersion(r.Context(), s.Subject, s.ObjectID, body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		h.failWopi(w, err)
		return
	}
	w.Header().Set("X-WOPI-ItemVersion", version)
	w.WriteHeader(http.StatusOK)
}

// failWopi maps a domain error to a bare WOPI status.
func (h *WopiHandler) failWopi(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, file.ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, file.ErrForbidden):
		w.WriteHeader(http.StatusForbidden)
	default:
		log.Printf("kamara: wopi: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}
