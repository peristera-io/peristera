// Package web holds Ergonomos's HTMX templates and rendering, separated
// from the HTTP handlers so the markup can be rendered headlessly for the
// accessibility check (ADR/README §4: automated a11y in CI) without a
// cluster or a login.
package web

import (
	"html/template"
	"io"

	"github.com/peristera-io/peristera/ergonomos/internal/task"
)

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
 body{font-family:system-ui,sans-serif;margin:2rem auto;max-width:40rem;color:#1c1917}
 header{display:flex;justify-content:space-between;align-items:baseline}
 ul{list-style:none;padding:0} li{display:flex;gap:.5rem;align-items:center;padding:.4rem 0;border-bottom:1px solid #e7e5e4}
 li.done label{text-decoration:line-through;color:#57534e} li form{margin:0}
 .grow{flex:1} form.add{display:flex;gap:.5rem;margin-top:1rem}
 button{cursor:pointer} a{color:#0f766e}
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
   <button type="submit" aria-label="{{if .Done}}{{msg "reopen"}}{{else}}{{msg "done"}}{{end}}: {{.Title}}">{{if .Done}}↺{{else}}✓{{end}}</button>
  </form>
  <label class="grow">{{.Title}}</label>
  <form hx-post="/tasks/{{.ID}}/delete" hx-target="#tasks" hx-swap="innerHTML">
   <button type="submit" aria-label="{{msg "delete"}}: {{.Title}}">✕</button>
  </form>
 </li>
 {{end}}
</ul>
{{end}}`))

// Data is the template model.
type Data struct{ Tasks []task.Task }

// Page renders the whole document.
func Page(w io.Writer, tasks []task.Task) error {
	return pageTmpl.Execute(w, Data{Tasks: tasks})
}

// List renders just the task-list fragment (the htmx swap target).
func List(w io.Writer, tasks []task.Task) error {
	return pageTmpl.ExecuteTemplate(w, "list", Data{Tasks: tasks})
}
