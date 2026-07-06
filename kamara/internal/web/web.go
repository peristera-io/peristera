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

//go:embed style.css htmx.min.js
var assets embed.FS

// Stylesheet returns the compiled Tailwind CSS (served at /style.css).
func Stylesheet() ([]byte, error) { return assets.ReadFile("style.css") }

// Script returns the vendored htmx runtime (served at /htmx.js) — embedded
// rather than pulled from a CDN, so the origin ships no third-party JS.
func Script() ([]byte, error) { return assets.ReadFile("htmx.min.js") }

// msg is the string catalog — no hardcoded strings in templates (README §4;
// EN only for now, FR/DE/LB are targets).
var msg = map[string]string{
	"title": "Kamara", "files": "Files", "logout": "Log out",
	"home": "Home", "empty": "This folder is empty.",
	"folder": "Folder", "file": "File", "open": "Open",
	"new_folder": "New folder", "upload": "Upload", "download": "Download",
	"rename": "Rename", "move": "Move", "delete": "Delete", "details": "Details",
	"name": "Name", "size": "Size", "breadcrumb": "Breadcrumb",
	"create": "Create", "move_to": "Move to", "confirm_delete": "Delete this item?",
	"location": "Location", "permalink": "Link", "created": "Created",
	"versions": "Versions", "versions_soon": "Version history is coming soon.",
	"close": "Close",
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
	// row bundles the context the per-item rename/move/delete controls need:
	// the route base ("/files" or "/folders"), the item, the current folder
	// (to re-render after the mutation), and the move-destination options.
	"row": func(base, id, name string, v View) rowCtx {
		return rowCtx{Base: base, ID: id, Name: name, At: v.Here(), Folders: v.AllFolders}
	},
}

// rowCtx is the template model for one item's action controls.
type rowCtx struct {
	Base, ID, Name, At string
	Folders            []file.Folder
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
	// AllFolders is every folder the caller owns — the move-destination
	// picker's options.
	AllFolders []file.Folder
	// Inline emits the stylesheet inline instead of a <link> — set by the
	// headless a11y render so contrast is evaluated.
	Inline bool
	// Drawer, when set, pre-renders the details drawer open — used by the
	// a11y render so the drawer markup is checked in context.
	Drawer *file.Object
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
<script src="/htmx.js" defer></script>
</head>
<body class="min-h-screen bg-stone-50 text-stone-900">
<header class="border-b border-stone-200 bg-white">
 <div class="mx-auto flex max-w-3xl items-center justify-between px-4 py-3">
  <a href="/" class="text-lg font-semibold text-brand">{{msg "title"}}</a>
  <a href="/auth/logout" class="text-sm text-stone-600 underline hover:text-stone-900">{{msg "logout"}}</a>
 </div>
</header>
<main id="browser" class="mx-auto max-w-3xl px-4 py-6">{{template "listing" .}}</main>
<aside id="drawer" aria-live="polite">{{if .Drawer}}{{template "details" .Drawer}}{{end}}</aside>
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
<div class="mb-4 flex flex-wrap items-end gap-4">
 <form hx-post="/folders?at={{.Here}}" hx-target="#browser" hx-swap="innerHTML"
       hx-on::after-request="if(event.detail.successful)this.reset()" class="flex items-end gap-2">
  <label class="text-sm text-stone-700">{{msg "new_folder"}}
   <input name="name" required class="mt-1 block rounded border border-stone-300 px-2 py-1 text-sm">
  </label>
  <button type="submit" class="rounded-base bg-brand px-3 py-1.5 text-sm font-medium text-white hover:opacity-90">{{msg "create"}}</button>
 </form>
 <form hx-post="/files?at={{.Here}}" hx-encoding="multipart/form-data" hx-target="#browser" hx-swap="innerHTML"
       hx-on::after-request="if(event.detail.successful)this.reset()" class="flex items-end gap-2">
  <label class="text-sm text-stone-700">{{msg "upload"}}
   <input type="file" name="file" required class="mt-1 block text-sm">
  </label>
  <button type="submit" class="rounded-base bg-brand px-3 py-1.5 text-sm font-medium text-white hover:opacity-90">{{msg "upload"}}</button>
 </form>
</div>
{{if and (not .Folders) (not .Files)}}
<p class="rounded-base border border-dashed border-stone-300 p-8 text-center text-stone-500">{{msg "empty"}}</p>
{{else}}
<ul class="divide-y divide-stone-200 rounded-base border border-stone-200 bg-white">
 {{range .Folders}}
 <li class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
  <span aria-hidden="true" class="text-brand">📁</span>
  <a href="/?folder={{.ID}}" hx-get="/browse?folder={{.ID}}" hx-target="#browser" hx-push-url="/?folder={{.ID}}"
     class="grow font-medium text-stone-900 underline-offset-2 hover:underline">{{.Name}}</a>
  <span class="text-xs uppercase tracking-wide text-stone-600">{{msg "folder"}}</span>
  {{template "rename" (row "/folders" .ID .Name $)}}
  {{template "moveto" (row "/folders" .ID .Name $)}}
  {{template "delete" (row "/folders" .ID .Name $)}}
 </li>
 {{end}}
 {{range .Files}}
 <li class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
  <span aria-hidden="true" class="text-stone-400">📄</span>
  <span class="grow font-medium text-stone-900">{{.Name}}</span>
  <span class="text-sm text-stone-500">{{bytes .Size}}</span>
  <a href="/files/{{.ID}}/download" class="text-sm text-brand underline">{{msg "download"}}</a>
  <button hx-get="/files/{{.ID}}/details" hx-target="#drawer" class="text-sm text-brand underline" aria-label="{{msg "details"}} {{.Name}}">{{msg "details"}}</button>
  {{template "rename" (row "/files" .ID .Name $)}}
  {{template "moveto" (row "/files" .ID .Name $)}}
  {{template "delete" (row "/files" .ID .Name $)}}
 </li>
 {{end}}
</ul>
{{end}}
{{end}}

{{define "rename"}}
<details class="text-sm">
 <summary class="cursor-pointer text-brand">{{msg "rename"}}</summary>
 <form hx-post="{{.Base}}/{{.ID}}/rename?at={{.At}}" hx-target="#browser" hx-swap="innerHTML" class="mt-2 flex items-center gap-2">
  <label class="sr-only">{{msg "name"}}</label>
  <input name="name" value="{{.Name}}" required class="rounded border border-stone-300 px-2 py-1">
  <button class="rounded-base bg-brand px-3 py-1.5 font-medium text-white hover:opacity-90">{{msg "rename"}}</button>
 </form>
</details>
{{end}}

{{define "moveto"}}
<details class="text-sm">
 <summary class="cursor-pointer text-brand">{{msg "move"}}</summary>
 <form hx-post="{{.Base}}/{{.ID}}/move?at={{.At}}" hx-target="#browser" hx-swap="innerHTML" class="mt-2 flex items-center gap-2">
  <label class="sr-only">{{msg "move_to"}}</label>
  <select name="dest" class="rounded border border-stone-300 px-2 py-1 text-sm">
   <option value="">{{msg "home"}}</option>
   {{range .Folders}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
  </select>
  <button class="rounded-base bg-brand px-3 py-1.5 text-sm font-medium text-white hover:opacity-90">{{msg "move"}}</button>
 </form>
</details>
{{end}}

{{define "delete"}}
<form hx-post="{{.Base}}/{{.ID}}/delete?at={{.At}}" hx-target="#browser" hx-swap="innerHTML" hx-confirm="{{msg "confirm_delete"}}">
 <button class="text-sm text-red-700 underline" aria-label="{{msg "delete"}} {{.Name}}">{{msg "delete"}}</button>
</form>
{{end}}

{{define "details"}}
<div role="dialog" aria-label="{{.Name}}" class="fixed inset-y-0 right-0 w-80 max-w-full overflow-y-auto border-l border-stone-200 bg-white p-4 shadow-lg">
 <div class="flex items-start justify-between gap-2">
  <h2 class="text-lg font-semibold text-stone-900">{{.Name}}</h2>
  <button onclick="this.closest('#drawer').innerHTML=''" class="text-sm text-stone-600 underline" aria-label="{{msg "close"}}">✕</button>
 </div>
 <dl class="mt-4 space-y-3 text-sm">
  <div><dt class="text-stone-500">{{msg "size"}}</dt><dd class="text-stone-900">{{bytes .Size}}</dd></div>
  <div><dt class="text-stone-500">{{msg "created"}}</dt><dd class="text-stone-900">{{.Created.Format "2006-01-02 15:04"}}</dd></div>
  <div><dt class="text-stone-500">{{msg "location"}}</dt>
   <dd><a href="/{{if .FolderID}}?folder={{.FolderID}}{{end}}" class="text-brand underline">{{msg "open"}}</a></dd></div>
  <div><dt class="text-stone-500">{{msg "permalink"}}</dt>
   <dd><a href="{{.Permalink}}" class="text-brand underline break-all">{{.Permalink}}</a></dd></div>
 </dl>
 <section class="mt-6">
  <h3 class="text-sm font-semibold text-stone-700">{{msg "versions"}}</h3>
  <p class="mt-1 text-sm text-stone-500">{{msg "versions_soon"}}</p>
 </section>
</div>
{{end}}`))

// Page renders the whole document for a folder listing.
func Page(w io.Writer, v View) error { return pageTmpl.Execute(w, v) }

// Listing renders just the browser fragment (the htmx swap target).
func Listing(w io.Writer, v View) error { return pageTmpl.ExecuteTemplate(w, "listing", v) }

// Details renders the file-details drawer fragment.
func Details(w io.Writer, o file.Object) error { return pageTmpl.ExecuteTemplate(w, "details", o) }
