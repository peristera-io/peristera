package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/web"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
)

func isNotFound(err error) bool  { return errors.Is(err, file.ErrNotFound) }
func isForbidden(err error) bool { return errors.Is(err, file.ErrForbidden) }

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
	return mux
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
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(o.Name))
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
	return web.View{Crumbs: crumbs, Folders: l.Folders, Files: l.Files}, nil
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

// fail maps a domain error to a status for the browser (plain, not JSON).
func (a *webApp) fail(w http.ResponseWriter, err error) {
	switch {
	case isNotFound(err):
		http.Error(w, "not found", http.StatusNotFound)
	case isForbidden(err):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
