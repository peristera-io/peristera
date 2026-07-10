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
	"log"
	"net/http"
	"strings"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/lib/pii"
)

// DefaultMaxUploadBytes caps a single upload body so one request cannot
// fill the tenant's blob volume (a per-tenant quota is the real answer —
// tracked separately). Overridable at construction.
const DefaultMaxUploadBytes int64 = 5 << 30 // 5 GiB

// Service is the file-domain surface the handlers use (satisfied by
// *file.Service; an interface so the handlers are testable with a fake).
type Service interface {
	Upload(ctx context.Context, owner pii.Subject, folderID *string, name string, r io.Reader) (file.Object, error)
	Download(ctx context.Context, caller pii.Subject, id string, w io.Writer) error
	List(ctx context.Context, caller pii.Subject) ([]file.Object, error)
	Get(ctx context.Context, caller pii.Subject, id string) (file.Object, error)
	Delete(ctx context.Context, caller pii.Subject, id string) error
	WriteVersion(ctx context.Context, caller pii.Subject, id string, r io.Reader) (string, error)
	ListVersions(ctx context.Context, caller pii.Subject, id string) ([]file.Version, error)

	CreateFolder(ctx context.Context, owner pii.Subject, parent *string, name string) (file.Folder, error)
	GetFolder(ctx context.Context, caller pii.Subject, id string) (file.Folder, error)
	ListChildren(ctx context.Context, caller pii.Subject, folder *string) (file.Listing, error)
	RenameFile(ctx context.Context, caller pii.Subject, id, name string) error
	MoveFile(ctx context.Context, caller pii.Subject, id string, dest *string) error
	RenameFolder(ctx context.Context, caller pii.Subject, id, name string) error
	MoveFolder(ctx context.Context, caller pii.Subject, id string, dest *string) error
	DeleteFolder(ctx context.Context, caller pii.Subject, id string) error
	DeleteFolderTree(ctx context.Context, caller pii.Subject, id string) error
	DownloadZip(ctx context.Context, caller pii.Subject, folder *string, w io.Writer) error
}

// Authenticator resolves a bearer token to the caller's subject. The
// production impl (userinfoAuth) validates against the tenant OIDC
// provider's userinfo endpoint; ok is false for a missing/invalid token.
type Authenticator interface {
	Subject(ctx context.Context, token string) (subject pii.Subject, ok bool, err error)
}

// Handler serves the storage API.
type Handler struct {
	svc       Service
	auth      Authenticator
	maxUpload int64
}

// New builds the handler. maxUpload caps the request body of an upload; <= 0
// uses DefaultMaxUploadBytes.
func New(svc Service, auth Authenticator, maxUpload int64) *Handler {
	if maxUpload <= 0 {
		maxUpload = DefaultMaxUploadBytes
	}
	return &Handler{svc: svc, auth: auth, maxUpload: maxUpload}
}

// Routes returns the mux for the /v1 surface. Mount it under "/v1/".
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/files", h.authed(h.list))
	mux.Handle("POST /v1/files", h.authed(h.upload))
	mux.Handle("GET /v1/files/{id}", h.authed(h.get))
	mux.Handle("DELETE /v1/files/{id}", h.authed(h.delete))
	mux.Handle("GET /v1/files/{id}/content", h.authed(h.download))
	mux.Handle("PUT /v1/files/{id}/content", h.authed(h.putContent))
	mux.Handle("GET /v1/files/{id}/versions", h.authed(h.listVersions))
	mux.Handle("POST /v1/files/{id}/rename", h.authed(h.renameFile))
	mux.Handle("POST /v1/files/{id}/move", h.authed(h.moveFile))
	// Folders (Kamara ADR-0002).
	mux.Handle("GET /v1/folders", h.authed(h.listChildren))
	mux.Handle("POST /v1/folders", h.authed(h.createFolder))
	mux.Handle("GET /v1/folders/{id}", h.authed(h.getFolder))
	mux.Handle("GET /v1/folders/{id}/zip", h.authed(h.downloadZip))
	mux.Handle("DELETE /v1/folders/{id}", h.authed(h.deleteFolder))
	mux.Handle("POST /v1/folders/{id}/rename", h.authed(h.renameFolder))
	mux.Handle("POST /v1/folders/{id}/move", h.authed(h.moveFolder))
	return mux
}

// optStr maps an empty string to nil (root), else a pointer to it.
func optStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
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
			// A provider hiccup is not the caller's fault and its detail
			// (issuer URL, transport error) must not leak to the client.
			log.Printf("kamara: authenticating: %v", err)
			writeErr(w, http.StatusBadGateway, "authentication unavailable")
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
	// Cap the body so a single request cannot fill the tenant volume. The
	// limit error surfaces from the chunker's first over-limit read.
	body := http.MaxBytesReader(w, r.Body, h.maxUpload)
	o, err := h.svc.Upload(r.Context(), caller, optStr(r.URL.Query().Get("folder")), name, body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
			return
		}
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
	w.Header().Set("Content-Type", ContentType(o.ContentType))
	w.Header().Set("Content-Disposition", ContentDisposition("attachment", o.Name))
	if err := h.svc.Download(r.Context(), caller, o.ID, w); err != nil {
		// The status and (some) bytes are already flushed, so this can only
		// be logged — writing a JSON error here would corrupt the byte
		// stream. Reassembly failures are integrity errors (a tampered or
		// missing blob), rare in practice; the client sees a short read.
		log.Printf("kamara: download %s: %v", o.ID, err)
		return
	}
}

// putContent replaces a file's content: the raw body becomes a new version
// (the file keeps its identity, owner, and URL — ADR-0007), same streaming
// discipline and size cap as upload.
func (h *Handler) putContent(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	body := http.MaxBytesReader(w, r.Body, h.maxUpload)
	if _, err := h.svc.WriteVersion(r.Context(), caller, r.PathValue("id"), body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "content exceeds maximum upload size")
			return
		}
		h.fail(w, err)
		return
	}
	o, err := h.svc.Get(r.Context(), caller, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toFile(o))
}

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	vs, err := h.svc.ListVersions(r.Context(), caller, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	versions := make([]versionDTO, 0, len(vs))
	for _, v := range vs {
		versions = append(versions, toVersion(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	if err := h.svc.Delete(r.Context(), caller, r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- folders + move/rename ---

func (h *Handler) listChildren(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	l, err := h.svc.ListChildren(r.Context(), caller, optStr(r.URL.Query().Get("parent")))
	if err != nil {
		h.fail(w, err)
		return
	}
	folders := make([]folderDTO, 0, len(l.Folders))
	for _, f := range l.Folders {
		folders = append(folders, toFolder(f))
	}
	files := make([]fileDTO, 0, len(l.Files))
	for _, o := range l.Files {
		files = append(files, toFile(o))
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "files": files})
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name query parameter is required")
		return
	}
	f, err := h.svc.CreateFolder(r.Context(), caller, optStr(r.URL.Query().Get("parent")), name)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toFolder(f))
}

// deleteFolder removes a folder — empty-first by default (the original
// contract), or the whole subtree with ?recursive=true.
func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	del := h.svc.DeleteFolder
	if r.URL.Query().Get("recursive") == "true" {
		del = h.svc.DeleteFolderTree
	}
	if err := del(r.Context(), caller, r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getFolder(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	f, err := h.svc.GetFolder(r.Context(), caller, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toFolder(f))
}

// downloadZip streams the folder's subtree as a zip archive named after the
// folder. Like download, a mid-stream failure can only be logged — the
// status and some bytes are already flushed.
func (h *Handler) downloadZip(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	id := r.PathValue("id")
	f, err := h.svc.GetFolder(r.Context(), caller, id) // authorizes + names the archive
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", ContentDisposition("attachment", f.Name+".zip"))
	if err := h.svc.DownloadZip(r.Context(), caller, &id, w); err != nil {
		log.Printf("kamara: zip %s: %v", id, err)
	}
}

func (h *Handler) renameFile(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	if err := h.svc.RenameFile(r.Context(), caller, r.PathValue("id"), name); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) moveFile(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	dest, ok := decodeMove(w, r, "folder")
	if !ok {
		return
	}
	if err := h.svc.MoveFile(r.Context(), caller, r.PathValue("id"), dest); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) renameFolder(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	if err := h.svc.RenameFolder(r.Context(), caller, r.PathValue("id"), name); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) moveFolder(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	dest, ok := decodeMove(w, r, "parent")
	if !ok {
		return
	}
	if err := h.svc.MoveFolder(r.Context(), caller, r.PathValue("id"), dest); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeName reads {"name": "..."} and validates it is non-empty.
func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return "", false
	}
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return "", false
	}
	return body.Name, true
}

// decodeMove reads the destination id under `field` from a JSON body:
// the field must be present — explicit null means "move to root", a value
// means that folder. Requiring presence avoids a typo'd field name silently
// yanking the item to the root.
func decodeMove(w http.ResponseWriter, r *http.Request, field string) (*string, bool) {
	var body map[string]*string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return nil, false
	}
	dest, present := body[field]
	if !present {
		writeErr(w, http.StatusBadRequest, `"`+field+`" field is required (use null for root)`)
		return nil, false
	}
	return dest, true
}

// fail maps a domain error to a status: not-found → 404, forbidden → 403,
// anything else → 500.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, file.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, file.ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, file.ErrNotEmpty):
		writeErr(w, http.StatusConflict, "folder not empty")
	case errors.Is(err, file.ErrCycle):
		writeErr(w, http.StatusBadRequest, "move would create a cycle")
	case errors.Is(err, file.ErrModified):
		writeErr(w, http.StatusConflict, "file was modified since the base version")
	default:
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

// fileDTO is the wire shape of components.schemas.File.
type fileDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	Folder      *string `json:"folder"`
	ContentType string  `json:"contentType"`
	Permalink   string  `json:"permalink"`
	Created     string  `json:"created"`
	Updated     string  `json:"updated"`
}

func toFile(o file.Object) fileDTO {
	return fileDTO{
		ID: o.ID, Name: o.Name, Size: o.Size, Folder: o.FolderID, ContentType: o.ContentType,
		Permalink: o.Permalink(),
		Created:   o.Created.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Updated:   o.Updated.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// versionDTO is the wire shape of components.schemas.Version.
type versionDTO struct {
	ID      string `json:"id"`
	Ordinal int    `json:"ordinal"`
	Size    int64  `json:"size"`
	Created string `json:"created"`
}

func toVersion(v file.Version) versionDTO {
	return versionDTO{ID: v.ID, Ordinal: v.Ordinal, Size: v.Size,
		Created: v.Created.UTC().Format("2006-01-02T15:04:05Z07:00")}
}

// folderDTO is the wire shape of a folder.
type folderDTO struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Parent    *string `json:"parent"`
	Permalink string  `json:"permalink"`
	Created   string  `json:"created"`
	Updated   string  `json:"updated"`
}

func toFolder(f file.Folder) folderDTO {
	return folderDTO{
		ID: f.ID, Name: f.Name, Parent: f.ParentID, Permalink: f.Permalink(),
		Created: f.Created.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Updated: f.Updated.UTC().Format("2006-01-02T15:04:05Z07:00"),
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

// ContentDisposition builds an RFC 6266 header value (#28): an ASCII
// "filename=" fallback plus, when the name has non-ASCII, an RFC 5987
// "filename*=UTF-8”" form so clients render the real (e.g. accented or
// non-Latin) name. Shared by the API download, the browser download, and
// (for the type) the WOPI host.
func ContentDisposition(disposition, name string) string {
	ascii := asciiFilename(name)
	v := disposition + `; filename="` + ascii + `"`
	if ascii != name {
		v += `; filename*=UTF-8''` + rfc5987(name)
	}
	return v
}

// asciiFilename strips control chars and quotes and replaces any non-ASCII
// byte with '_' for the plain filename= fallback.
func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || r == '"' || r == '\\':
			// drop control chars, quotes, backslash
		case r > 0x7f:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// rfc5987 percent-encodes name per RFC 5987 (UTF-8), leaving only the
// attr-char set unescaped.
func rfc5987(name string) string {
	const attr = "!#$&+-.^_`|~"
	var b strings.Builder
	for _, c := range []byte(name) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			strings.IndexByte(attr, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte("0123456789ABCDEF"[c>>4])
		b.WriteByte("0123456789ABCDEF"[c&0x0f])
	}
	return b.String()
}

// ContentType returns the stored MIME type or the octet-stream fallback (#28).
func ContentType(stored string) string {
	if stored == "" {
		return "application/octet-stream"
	}
	return stored
}
