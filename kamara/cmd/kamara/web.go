package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/peristera-io/peristera/kamara/internal/api"
	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/web"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
)

// webApp is the browser file UI: OIDC cookie session (via the relying party)
// resolves the logged-in user to a subject, and the file service does the
// work. It sits beside the bearer /v1 API — same service, two front doors.
type webApp struct {
	svc      *file.Service
	rp       *oidcrp.RelyingParty
	instance string // the tenant domain (issuer host), the subject's instance
}

// routes are the guarded browser routes (mounted behind rp.Middleware). The
// browser surface is cookie-authed end to end — it never links to the bearer
// /v1 API (which is for machine callers).
func (a *webApp) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.page)                     // full page at the root
	mux.HandleFunc("GET /browse", a.frag)                  // htmx fragment (folder navigation)
	mux.HandleFunc("GET /files/{id}/download", a.download) // cookie-authed download
	mux.HandleFunc("GET /files/{id}/details", a.details)   // details drawer fragment
	// Mutations — POST forms (HTML forms are GET/POST only). CSRF is closed
	// by the SameSite=Lax session cookie: a cross-site POST omits the cookie,
	// so the request is unauthenticated and rejected. Each re-renders the
	// current folder (?at=) as the htmx swap target.
	mux.HandleFunc("POST /folders", a.createFolder)
	mux.HandleFunc("POST /folders/{id}/rename", a.renameFolder)
	mux.HandleFunc("POST /folders/{id}/move", a.moveFolder)
	mux.HandleFunc("POST /folders/{id}/delete", a.deleteFolder)
	mux.HandleFunc("POST /files", a.upload)
	mux.HandleFunc("POST /files/{id}/rename", a.renameFile)
	mux.HandleFunc("POST /files/{id}/move", a.moveFile)
	mux.HandleFunc("POST /files/{id}/delete", a.deleteFile)
	return mux
}

// optStr maps an empty string to nil (root), else a pointer to it.
func optStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// authed resolves the session subject or writes a 401 (an htmx POST won't
// swap on a non-2xx, so a lost session simply no-ops rather than corrupting
// the page).
func (a *webApp) authed(w http.ResponseWriter, r *http.Request) (pii.Subject, bool) {
	caller, ok := a.caller(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
	return caller, ok
}

// renderAt re-renders the current folder's listing fragment (the ?at= folder,
// empty = root) after a mutation.
func (a *webApp) renderAt(w http.ResponseWriter, r *http.Request, caller pii.Subject) {
	v, err := a.view(r.Context(), caller, r.URL.Query().Get("at"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Listing(w, v)
}

func (a *webApp) createFolder(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if _, err := a.svc.CreateFolder(r.Context(), caller, optStr(r.URL.Query().Get("at")), r.FormValue("name")); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) upload(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	folder := optStr(r.URL.Query().Get("at"))
	// Cap the WHOLE request body (every part + headers), not just the file
	// part, so a malicious multipart can't stream unbounded non-file data.
	r.Body = http.MaxBytesReader(w, r.Body, api.DefaultMaxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected a multipart upload", http.StatusBadRequest)
		return
	}
	uploaded := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			a.fail(w, err) // includes MaxBytesError → 413
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			continue
		}
		if _, err := a.svc.Upload(r.Context(), caller, folder, part.FileName(), part); err != nil {
			a.fail(w, err)
			return
		}
		uploaded = true
		break
	}
	if !uploaded {
		http.Error(w, "no file provided", http.StatusBadRequest)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) renameFile(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.RenameFile(r.Context(), caller, r.PathValue("id"), r.FormValue("name")); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) moveFile(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.MoveFile(r.Context(), caller, r.PathValue("id"), optStr(r.FormValue("dest"))); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) deleteFile(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.Delete(r.Context(), caller, r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) renameFolder(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.RenameFolder(r.Context(), caller, r.PathValue("id"), r.FormValue("name")); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) moveFolder(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.MoveFolder(r.Context(), caller, r.PathValue("id"), optStr(r.FormValue("dest"))); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

func (a *webApp) deleteFolder(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.DeleteFolder(r.Context(), caller, r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	a.renderAt(w, r, caller)
}

// download streams a file to the logged-in browser (cookie session), so the
// UI needs no bearer token. Authorization is the same can_access check.
func (a *webApp) download(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	id := r.PathValue("id")
	o, err := a.svc.Get(r.Context(), caller, id) // authorizes + gives the name
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", api.ContentType(o.ContentType))
	w.Header().Set("Content-Disposition", api.ContentDisposition("attachment", o.Name))
	if err := a.svc.Download(r.Context(), caller, id, w); err != nil {
		// Status/bytes may be flushed; only log (a JSON error would corrupt
		// the stream). Integrity errors are rare.
		log.Printf("kamara web: download %s: %v", id, err)
	}
}

// caller resolves the logged-in browser session to a subject. The relying
// party's middleware guarantees a session on these routes.
func (a *webApp) caller(r *http.Request) (pii.Subject, bool) {
	sess, ok := a.rp.Session(r)
	if !ok {
		return pii.Subject{}, false
	}
	return pii.Subject{Instance: a.instance, UserID: sess.Claims.Subject}, true
}

// view builds the listing model for the folder named by ?folder= (empty =
// root): its ancestors (breadcrumb) and its children.
func (a *webApp) view(ctx context.Context, caller pii.Subject, folder string) (web.View, error) {
	var crumbs []file.Folder
	var fptr *string
	if folder != "" {
		fptr = &folder
		c, err := a.svc.Ancestors(ctx, caller, folder)
		if err != nil {
			return web.View{}, err
		}
		crumbs = c
	}
	l, err := a.svc.ListChildren(ctx, caller, fptr)
	if err != nil {
		return web.View{}, err
	}
	all, err := a.svc.AllFolders(ctx, caller)
	if err != nil {
		return web.View{}, err
	}
	return web.View{Crumbs: crumbs, Folders: l.Folders, Files: l.Files, AllFolders: all}, nil
}

func (a *webApp) page(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	v, err := a.view(r.Context(), caller, r.URL.Query().Get("folder"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Page(w, v)
}

func (a *webApp) frag(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	v, err := a.view(r.Context(), caller, r.URL.Query().Get("folder"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Listing(w, v)
}

// details renders the file-details drawer fragment (authorized via Get).
func (a *webApp) details(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	o, err := a.svc.Get(r.Context(), caller, r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Details(w, o)
}

// fail maps a domain error to a status for the browser (plain, not JSON).
// htmx does not swap a non-2xx response, so a failed mutation leaves the
// listing unchanged; richer inline error messaging is M4c polish.
func (a *webApp) fail(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "file exceeds maximum upload size", http.StatusRequestEntityTooLarge)
		return
	}
	switch {
	case errors.Is(err, file.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, file.ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, file.ErrNotEmpty):
		http.Error(w, "folder not empty", http.StatusConflict)
	case errors.Is(err, file.ErrCycle):
		http.Error(w, "move would create a cycle", http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
