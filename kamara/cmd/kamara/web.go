package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/peristera-io/peristera/kamara/internal/api"
	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/web"
	"github.com/peristera-io/peristera/kamara/internal/wopi"
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
	// Office editing (ADR-0018), set only when the tenant enabled the office
	// engine. sessions mints the per-file WOPI access token; discovery resolves
	// the engine's editor URL; wopiSrcBase is Kamara's own in-cluster base (the
	// WOPISrc the engine fetches back). office is the engine's public URL (also
	// the "is office on?" flag).
	sessions    *wopi.Sessions
	discovery   *wopi.Discovery
	office      string
	wopiSrcBase string
}

// officeEnabled reports whether the tenant has the office engine (ADR-0018).
func (a *webApp) officeEnabled() bool { return a.office != "" }

// routes are the guarded browser routes (mounted behind rp.Middleware). The
// browser surface is cookie-authed end to end — it never links to the bearer
// /v1 API (which is for machine callers).
func (a *webApp) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.page)                     // full page at the root
	mux.HandleFunc("GET /browse", a.frag)                  // htmx fragment (folder navigation)
	mux.HandleFunc("GET /files/{id}/download", a.download) // cookie-authed download
	mux.HandleFunc("GET /files/{id}/preview", a.preview)   // inline image preview (drawer)
	mux.HandleFunc("GET /files/{id}/details", a.details)   // details drawer fragment
	mux.HandleFunc("GET /edit/{id}", a.edit)               // office editor page (ADR-0018)
	mux.HandleFunc("GET /text/{id}", a.textEditor)         // plain-text editor page
	mux.HandleFunc("GET /zip", a.zip)                      // folder subtree (?at=, empty = root) as a zip
	// Mutations — POST forms (HTML forms are GET/POST only). CSRF is closed
	// by the SameSite=Lax session cookie: a cross-site POST omits the cookie,
	// so the request is unauthenticated and rejected. Each re-renders the
	// current folder (?at=) as the htmx swap target.
	mux.HandleFunc("POST /folders", a.createFolder)
	mux.HandleFunc("POST /folders/{id}/rename", a.renameFolder)
	mux.HandleFunc("POST /folders/{id}/move", a.moveFolder)
	mux.HandleFunc("POST /folders/{id}/delete", a.deleteFolder)
	mux.HandleFunc("POST /files", a.upload)
	mux.HandleFunc("POST /files/new", a.newTextFile) // create an empty text file, then open the editor
	mux.HandleFunc("POST /files/{id}/rename", a.renameFile)
	mux.HandleFunc("POST /files/{id}/move", a.moveFile)
	mux.HandleFunc("POST /files/{id}/delete", a.deleteFile)
	mux.HandleFunc("POST /text/{id}", a.textSave) // text editor save (PRG)
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

// deleteFolder removes a folder and everything in it — the browser flow is
// recursive (like OneDrive/Dropbox), guarded by its own scarier hx-confirm;
// the empty-first contract stays available on the /v1 API.
func (a *webApp) deleteFolder(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	if err := a.svc.DeleteFolderTree(r.Context(), caller, r.PathValue("id")); err != nil {
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

// preview streams an image inline for the drawer's thumbnail. Only
// non-SVG image/* is ever rendered inline: an HTML or SVG document served
// on this cookie-authed origin could carry script (stored XSS) — everything
// else stays an attachment via /download.
func (a *webApp) preview(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	o, err := a.svc.Get(r.Context(), caller, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !o.Previewable() {
		http.Error(w, "not previewable", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", o.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", api.ContentDisposition("inline", o.Name))
	if err := a.svc.Download(r.Context(), caller, id, w); err != nil {
		log.Printf("kamara web: preview %s: %v", id, err)
	}
}

// zip streams the ?at= folder's subtree (empty = the whole root) as a zip
// archive named after the folder. Mid-stream failures can only be logged —
// the status and some bytes are already flushed.
func (a *webApp) zip(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	name := "kamara"
	var folder *string
	if at := r.URL.Query().Get("at"); at != "" {
		f, err := a.svc.GetFolder(r.Context(), caller, at) // authorizes + names the archive
		if err != nil {
			a.fail(w, err)
			return
		}
		name = f.Name
		folder = &at
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", api.ContentDisposition("attachment", name+".zip"))
	if err := a.svc.DownloadZip(r.Context(), caller, folder, w); err != nil {
		log.Printf("kamara web: zip %v: %v", r.URL.Query().Get("at"), err)
	}
}

// textEditor renders the plain-text editor page: the file's content in a
// textarea plus the version ordinal it was loaded at (the save's optimistic-
// concurrency base). Distinct from the office editor (/edit — an iframe on
// the WOPI engine); this one is Kamara's own and needs no engine.
func (a *webApp) textEditor(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	id := r.PathValue("id")
	o, err := a.svc.Get(r.Context(), caller, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !o.TextEditable() {
		http.Error(w, "not editable as text", http.StatusUnsupportedMediaType)
		return
	}
	var buf bytes.Buffer
	if err := a.svc.Download(r.Context(), caller, id, &buf); err != nil {
		a.fail(w, err)
		return
	}
	if !utf8.Valid(buf.Bytes()) {
		http.Error(w, "not a text file", http.StatusUnsupportedMediaType)
		return
	}
	base, err := a.latestOrdinal(r.Context(), caller, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.TextEditor(w, web.TextEditorView{Object: o, Content: buf.String(), Base: base,
		Saved: r.URL.Query().Get("saved") == "1"})
}

// textSave writes the textarea back as a new version, guarded by the base
// ordinal the editor was loaded at. Success redirects back to the editor
// (PRG — a reload never re-saves); a conflict re-renders the editor with the
// user's content, the new base, and an alert, so saving again is a
// deliberate overwrite.
func (a *webApp) textSave(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	o, err := a.svc.Get(r.Context(), caller, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !o.TextEditable() {
		http.Error(w, "not editable as text", http.StatusUnsupportedMediaType)
		return
	}
	// The form carries the whole content — bound like uploads, sized for the
	// editor cap plus form overhead.
	r.Body = http.MaxBytesReader(w, r.Body, 2*file.MaxTextEditBytes)
	base, err := strconv.Atoi(r.FormValue("base"))
	if err != nil {
		http.Error(w, "invalid base version", http.StatusBadRequest)
		return
	}
	// Textareas submit CRLF line endings; store what the user sees.
	content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")
	if _, err := a.svc.WriteVersionAt(r.Context(), caller, id, base, strings.NewReader(content)); err != nil {
		if errors.Is(err, file.ErrModified) {
			latest, lerr := a.latestOrdinal(r.Context(), caller, id)
			if lerr != nil {
				a.fail(w, lerr)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			_ = web.TextEditor(w, web.TextEditorView{Object: o, Content: content, Base: latest, Conflict: true})
			return
		}
		a.fail(w, err)
		return
	}
	http.Redirect(w, r, "/text/"+id+"?saved=1", http.StatusSeeOther)
}

// newTextFile creates an empty file in the current folder and opens it in
// the text editor (falling back to the folder view for a name the editor
// cannot open, e.g. "photo.png").
func (a *webApp) newTextFile(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.authed(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	at := r.URL.Query().Get("at")
	o, err := a.svc.Upload(r.Context(), caller, optStr(at), name, strings.NewReader(""))
	if err != nil {
		a.fail(w, err)
		return
	}
	if !o.TextEditable() {
		back := "/"
		if at != "" {
			back = "/?folder=" + url.QueryEscape(at)
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/text/"+o.ID, http.StatusSeeOther)
}

// latestOrdinal returns the file's newest version ordinal (0 when it has
// only its initial version) — the text editor's concurrency base.
func (a *webApp) latestOrdinal(ctx context.Context, caller pii.Subject, id string) (int, error) {
	vs, err := a.svc.ListVersions(ctx, caller, id)
	if err != nil {
		return 0, err
	}
	if len(vs) == 0 { // defensive: every object gets version 0 on upload
		return 0, nil
	}
	return vs[0].Ordinal, nil // newest first
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
	id := r.PathValue("id")
	o, err := a.svc.Get(r.Context(), caller, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	versions, err := a.svc.ListVersions(r.Context(), caller, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	latest := 0
	if len(versions) > 0 { // ListVersions is newest-first
		latest = versions[0].Ordinal
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Details(w, web.DetailView{Object: o, Versions: versions, Latest: latest, Office: a.officeEnabled()})
}

// edit renders the office editor page for a file: authorize, mint a per-session
// WOPI access token, resolve the engine's editor URL, and return an
// auto-submitting form that embeds the Collabora iframe (ADR-0018). The engine
// then calls back to Kamara's /wopi host with the token.
func (a *webApp) edit(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if !a.officeEnabled() {
		http.Error(w, "office editing is not enabled", http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	o, err := a.svc.Get(r.Context(), caller, id) // authorizes + gives the name/type
	if err != nil {
		a.fail(w, err)
		return
	}
	// Mint an opaque, per-session token scoped to (file, user, write, TTL); the
	// engine presents it on every WOPI call and Kamara re-checks OpenFGA.
	// canWrite is true because only the owner can pass svc.Get today; once
	// read-only shares exist (#33) this must derive write from the caller's
	// grant, not hardcode it.
	token, err := a.sessions.Mint(r.Context(), caller, id, true)
	if err != nil {
		log.Printf("kamara web: mint wopi token %s: %v", id, err)
		http.Error(w, "could not open editor", http.StatusInternalServerError)
		return
	}
	// WOPISrc is Kamara's own in-cluster address — the engine fetches it back
	// intra-namespace (R68). Resolve the editor URL from the engine's discovery.
	wopiSrc := a.wopiSrcBase + "/wopi/files/" + id
	actionURL, err := a.discovery.EditURL(r.Context(), fileExt(o.Name), wopiSrc)
	if err != nil {
		log.Printf("kamara web: editor url for %s: %v", id, err)
		http.Error(w, "no editor for this file type", http.StatusUnsupportedMediaType)
		return
	}
	// The page body carries the WOPI access token — keep it out of any cache.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Editor(w, web.EditorView{
		Name:           o.Name,
		ActionURL:      actionURL,
		AccessToken:    token,
		AccessTokenTTL: 0, // 0 = the host does not expire the token client-side; server TTL governs
	})
}

// fileExt returns a file name's extension without the dot ("" if none).
func fileExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return ""
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
	case errors.Is(err, file.ErrModified):
		http.Error(w, "file was modified since the base version", http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
