package main

import (
	"html/template"
	"net/http"

	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
)

// webApp is the HTMX UI over the task service. Minimal but pleasant
// (README §5); the a11y CI gate and polish land in M3b session 6.
type webApp struct {
	svc      *task.Service
	rp       *oidcrp.RelyingParty
	instance string // tenant issuer host — the subject's home instance
	issuer   string
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
	return mux
}

// msg is the string catalog — no hardcoded strings in templates (README §4;
// EN only for now, FR/DE/LB are targets).
var msg = map[string]string{
	"title": "Ergonomos", "tasks": "Tasks", "add": "Add task",
	"new_placeholder": "What needs doing?", "logout": "Log out",
	"done": "Done", "reopen": "Reopen", "delete": "Delete", "empty": "No tasks yet.",
}

var funcs = template.FuncMap{"msg": func(k string) string {
	if v, ok := msg[k]; ok {
		return v
	}
	return "⟪" + k + "⟫"
}}

var pageTmpl = template.Must(template.New("page").Funcs(funcs).Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{msg "title"}}</title>
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
<style>
 body{font-family:system-ui,sans-serif;margin:2rem auto;max-width:40rem}
 header{display:flex;justify-content:space-between;align-items:baseline}
 ul{list-style:none;padding:0} li{display:flex;gap:.5rem;align-items:center;padding:.4rem 0;border-bottom:1px solid #eee}
 li.done label{text-decoration:line-through;color:#777} li form{margin:0}
 .grow{flex:1} form.add{display:flex;gap:.5rem;margin-top:1rem}
</style></head>
<body>
<header><h1>{{msg "title"}}</h1><a href="/auth/logout">{{msg "logout"}}</a></header>
<main id="tasks">{{template "list" .}}</main>
<form class="add" hx-post="/tasks" hx-target="#tasks" hx-swap="innerHTML">
 <input class="grow" name="title" placeholder="{{msg "new_placeholder"}}" required aria-label="{{msg "new_placeholder"}}">
 <button type="submit">{{msg "add"}}</button>
</form>
</body></html>
{{define "list"}}
<h2>{{msg "tasks"}}</h2>
{{if not .Tasks}}<p>{{msg "empty"}}</p>{{end}}
<ul>
 {{range .Tasks}}
 <li{{if .Done}} class="done"{{end}} id="task-{{.ID}}">
  <form hx-post="/tasks/{{.ID}}/done" hx-target="#tasks" hx-swap="innerHTML">
   <input type="hidden" name="done" value="{{if .Done}}false{{else}}true{{end}}">
   <button type="submit" aria-label="{{if .Done}}{{msg "reopen"}}{{else}}{{msg "done"}}{{end}}">{{if .Done}}↺{{else}}✓{{end}}</button>
  </form>
  <label class="grow">{{.Title}}</label>
  <form hx-post="/tasks/{{.ID}}/delete" hx-target="#tasks" hx-swap="innerHTML">
   <button type="submit" aria-label="{{msg "delete"}}">✕</button>
  </form>
 </li>
 {{end}}
</ul>
{{end}}`))

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
	data := map[string]any{"Tasks": tasks}
	if whole {
		_ = pageTmpl.Execute(w, data)
	} else {
		_ = pageTmpl.ExecuteTemplate(w, "list", data)
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
		http.Error(w, err.Error(), http.StatusForbidden)
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
