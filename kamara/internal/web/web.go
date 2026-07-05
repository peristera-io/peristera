// Package web holds Kamara's HTMX browser UI — templates, the string
// catalog, and the compiled stylesheet — separated from the HTTP handlers so
// the markup can be rendered headlessly for the accessibility check (README
// §4: automated a11y in CI) without a cluster or a login.
//
// Styling is Tailwind (the Peristera design-language pilot): the stylesheet
// is generated from these templates by the Tailwind CLI (see web/ and
// `make css`) and embedded, so the runtime image stays a single static
// binary with no Node (Q&A R9 / M4b plan).
package web

import (
	"embed"
	"html/template"
	"io"
	"strconv"

	"github.com/peristera-io/peristera/kamara/internal/file"
)

//go:embed style.css
var assets embed.FS

// Stylesheet returns the compiled Tailwind CSS (served at /style.css).
func Stylesheet() ([]byte, error) { return assets.ReadFile("style.css") }

// msg is the string catalog — no hardcoded strings in templates (README §4;
// EN only for now, FR/DE/LB are targets).
var msg = map[string]string{
	"title": "Kamara", "files": "Files", "logout": "Log out",
	"home": "Home", "empty": "This folder is empty.",
	"folder": "Folder", "file": "File", "open": "Open",
	"new_folder": "New folder", "upload": "Upload", "download": "Download",
	"rename": "Rename", "move": "Move", "delete": "Delete", "details": "Details",
	"name": "Name", "size": "Size", "breadcrumb": "Breadcrumb",
}

var funcs = template.FuncMap{
	"msg": func(k string) string {
		if v, ok := msg[k]; ok {
			return v
		}
		return "⟪" + k + "⟫"
	},
	"bytes": humanBytes,
	// stylesheet inlines the compiled CSS (used by the headless a11y render
	// so axe evaluates real colour contrast; production links /style.css).
	"stylesheet": func() template.CSS {
		b, _ := assets.ReadFile("style.css")
		return template.CSS(b)
	},
}

// humanBytes renders a size for display (IEC units).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "iB"
}

// View is the template model for one folder listing.
type View struct {
	// Crumbs are the ancestor folders root-first; the current folder is the
	// last entry. Empty at the root.
	Crumbs  []file.Folder
	Folders []file.Folder
	Files   []file.Object
	// Inline emits the stylesheet inline instead of a <link> — set by the
	// headless a11y render so contrast is evaluated.
	Inline bool
}

// Here is the id of the folder currently shown ("" = root).
func (v View) Here() string {
	if len(v.Crumbs) == 0 {
		return ""
	}
	return v.Crumbs[len(v.Crumbs)-1].ID
}

var pageTmpl = template.Must(template.New("page").Funcs(funcs).Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{msg "title"}}</title>
{{if .Inline}}<style>{{stylesheet}}</style>{{else}}<link rel="stylesheet" href="/style.css">{{end}}
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
</head>
<body class="min-h-screen bg-stone-50 text-stone-900">
<header class="border-b border-stone-200 bg-white">
 <div class="mx-auto flex max-w-3xl items-center justify-between px-4 py-3">
  <a href="/" class="text-lg font-semibold text-brand">{{msg "title"}}</a>
  <a href="/auth/logout" class="text-sm text-stone-600 underline hover:text-stone-900">{{msg "logout"}}</a>
 </div>
</header>
<main id="browser" class="mx-auto max-w-3xl px-4 py-6">{{template "listing" .}}</main>
</body></html>
{{define "listing"}}
<nav aria-label="{{msg "breadcrumb"}}" class="mb-4 text-sm text-stone-600">
 <ol class="flex flex-wrap items-center gap-1">
  <li><a href="/" hx-get="/browse" hx-target="#browser" hx-push-url="/" class="underline hover:text-brand">{{msg "home"}}</a></li>
  {{range .Crumbs}}
  <li aria-hidden="true" class="text-stone-400">/</li>
  <li><a href="/?folder={{.ID}}" hx-get="/browse?folder={{.ID}}" hx-target="#browser" hx-push-url="/?folder={{.ID}}" class="underline hover:text-brand">{{.Name}}</a></li>
  {{end}}
 </ol>
</nav>
<h1 class="sr-only">{{msg "files"}}</h1>
{{if and (not .Folders) (not .Files)}}
<p class="rounded-base border border-dashed border-stone-300 p-8 text-center text-stone-500">{{msg "empty"}}</p>
{{else}}
<ul class="divide-y divide-stone-200 rounded-base border border-stone-200 bg-white">
 {{range .Folders}}
 <li class="flex items-center gap-3 px-4 py-3">
  <span aria-hidden="true" class="text-brand">📁</span>
  <a href="/?folder={{.ID}}" hx-get="/browse?folder={{.ID}}" hx-target="#browser" hx-push-url="/?folder={{.ID}}"
     class="grow font-medium text-stone-900 underline-offset-2 hover:underline">{{.Name}}</a>
  <span class="text-xs uppercase tracking-wide text-stone-600">{{msg "folder"}}</span>
 </li>
 {{end}}
 {{range .Files}}
 <li class="flex items-center gap-3 px-4 py-3">
  <span aria-hidden="true" class="text-stone-400">📄</span>
  <span class="grow font-medium text-stone-900">{{.Name}}</span>
  <span class="text-sm text-stone-500">{{bytes .Size}}</span>
  <a href="/v1/files/{{.ID}}/content" class="text-sm text-brand underline">{{msg "download"}}</a>
 </li>
 {{end}}
</ul>
{{end}}
{{end}}`))

// Page renders the whole document for a folder listing.
func Page(w io.Writer, v View) error { return pageTmpl.Execute(w, v) }

// Listing renders just the browser fragment (the htmx swap target).
func Listing(w io.Writer, v View) error { return pageTmpl.ExecuteTemplate(w, "listing", v) }
