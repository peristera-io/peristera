package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/peristera-io/peristera/ergonomos/internal/kamara"
	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/ergonomos/internal/web"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
)

// statusFor maps a service error to an HTTP status: a missing task is 404,
// everything else (authorization, store errors) is 403 — the stub's coarse
// mapping, kept simple for M3.
func statusFor(err error) int {
	if errors.Is(err, task.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusForbidden
}

// webApp is the HTMX UI over the task service. Rendering lives in
// internal/web (so it can be a11y-checked headlessly); this file is the
// HTTP wiring.
type webApp struct {
	svc      *task.Service
	rp       *oidcrp.RelyingParty
	instance string // tenant issuer host — the subject's home instance
	issuer   string
	kamara   *kamara.Client // on-behalf-of file attach (ADR-0017); nil if not a caller
}

// caller resolves the logged-in user to a data subject (ADR-0009 §2).
func (a *webApp) caller(r *http.Request) (pii.Subject, bool) {
	sess, ok := a.rp.Session(r)
	if !ok {
		return pii.Subject{}, false
	}
	return pii.Subject{Instance: a.instance, UserID: sess.Claims.Subject}, true
}

func (a *webApp) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("POST /tasks", a.create)
	mux.HandleFunc("POST /tasks/{id}/done", a.setDone)
	mux.HandleFunc("POST /tasks/{id}/delete", a.delete)
	mux.HandleFunc("POST /attach", a.attach)
	return mux
}

// attach uploads the request body to Kamara on behalf of the logged-in user
// (ADR-0017): Ergonomos exchanges the user's token and calls Kamara's storage
// API, so the file lands owned by the user. The acceptance for the S2S model.
func (a *webApp) attach(w http.ResponseWriter, r *http.Request) {
	if a.kamara == nil {
		http.Error(w, "attach not configured", http.StatusNotImplemented)
		return
	}
	sess, ok := a.rp.Session(r)
	if !ok {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	if sess.AccessToken == "" {
		http.Error(w, "no access token to exchange", http.StatusForbidden)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "attachment"
	}
	id, err := a.kamara.Upload(r.Context(), sess.AccessToken, name, r.Body)
	if err != nil {
		// Detail (issuer, exchange/upload error) must not leak to the client.
		log.Printf("ergonomos: attach: %v", err)
		http.Error(w, "attach failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"fileId": id})
}

// render lists the caller's tasks (permission-filtered) and writes either
// the whole page or just the list fragment (the htmx swap target).
func (a *webApp) render(w http.ResponseWriter, r *http.Request, whole bool) {
	caller, ok := a.caller(r)
	if !ok {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	tasks, err := a.svc.List(r.Context(), caller)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if whole {
		_ = web.Page(w, tasks)
	} else {
		_ = web.List(w, tasks)
	}
}

func (a *webApp) index(w http.ResponseWriter, r *http.Request) { a.render(w, r, true) }

func (a *webApp) create(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	if _, err := a.svc.Create(r.Context(), caller, r.FormValue("title")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.render(w, r, false)
}

func (a *webApp) setDone(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	done := r.FormValue("done") == "true"
	if _, err := a.svc.SetDone(r.Context(), caller, r.PathValue("id"), done); err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	a.render(w, r, false)
}

func (a *webApp) delete(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	if err := a.svc.Delete(r.Context(), caller, r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	a.render(w, r, false)
}
