package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/lib/pii"
)

func sampleView() View {
	owner := pii.Subject{Instance: "demo.example", UserID: "alice"}
	now := time.Unix(0, 0).UTC()
	here := "fid"
	return View{
		Crumbs:     []file.Folder{{ID: here, Owner: owner, Name: "Projects"}},
		Folders:    []file.Folder{{ID: "sub", Owner: owner, Name: "Designs", ParentID: &here}},
		Files:      []file.Object{{ID: "f1", Owner: owner, Name: "report.pdf", Size: 2048, FolderID: &here, Created: now, Updated: now}},
		AllFolders: []file.Folder{{ID: here, Owner: owner, Name: "Projects"}, {ID: "sub", Owner: owner, Name: "Designs"}},
	}
}

func TestPageRendersDocument(t *testing.T) {
	var b bytes.Buffer
	if err := Page(&b, sampleView()); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		"<!doctype html>", `<html lang="en">`, "Kamara",
		"Projects",                    // breadcrumb
		"Designs",                     // folder
		"report.pdf",                  // file
		"2.0 KiB",                     // humanized size
		`href="/files/f1/download"`,   // cookie-authed download link
		`hx-get="/browse?folder=sub"`, // htmx folder navigation
		`href="/style.css"`,           // linked stylesheet (not inlined)
		`src="/htmx.js"`,              // vendored htmx (not a CDN)
		`src="/kamara.js"`,            // uploader component
		"<kamara-uploader",            // progressive-enhancement wrapper around the upload form
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestOperationsMarkup(t *testing.T) {
	var b bytes.Buffer
	if err := Listing(&b, sampleView()); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		`hx-post="/folders?at=fid"`,            // create-folder form, into the current folder
		`hx-post="/files?at=fid"`,              // upload form, into the current folder
		`hx-encoding="multipart/form-data"`,    // file upload encoding
		`hx-post="/folders/sub/rename?at=fid"`, // rename a folder
		`hx-post="/folders/sub/move?at=fid"`,   // move a folder
		`hx-post="/folders/sub/delete?at=fid"`, // delete a folder
		`hx-post="/files/f1/rename?at=fid"`,    // rename a file
		`hx-post="/files/f1/move?at=fid"`,      // move a file
		`hx-post="/files/f1/delete?at=fid"`,    // delete a file
		`hx-confirm=`,                          // delete confirmation
		`name="dest"`,                          // move-destination picker
		`aria-label="Delete report.pdf"`,       // accessible delete button name
		`hx-get="/files/f1/details"`,           // details drawer trigger
	} {
		if !strings.Contains(html, want) {
			t.Errorf("operations markup missing %q", want)
		}
	}
}

func TestDetailsDrawer(t *testing.T) {
	var b bytes.Buffer
	o := file.Object{ID: "f1", Name: "report.pdf", Size: 2048, Created: time.Unix(0, 0).UTC()}
	v := DetailView{Object: o, Office: true, Latest: 1, Versions: []file.Version{
		{Ordinal: 1, Size: 2048, Created: time.Unix(0, 0).UTC()},
		{Ordinal: 0, Size: 1024, Created: time.Unix(0, 0).UTC()},
	}}
	if err := Details(&b, v); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		`role="region"`, "data-drawer", "report.pdf", "2.0 KiB",
		msg["versions"], msg["version_current"], // real version history now
		"v1", "v0", // two versions listed
		msg["edit_office"], // Edit button (office enabled)
		`href="/edit/f1"`,  // links to the editor
		`href="/files/f1"`, // permalink
	} {
		if !strings.Contains(html, want) {
			t.Errorf("details drawer missing %q", want)
		}
	}
}

func TestDetailsDrawerNoOfficeNoVersions(t *testing.T) {
	var b bytes.Buffer
	o := file.Object{ID: "f1", Name: "report.pdf", Size: 2048, Created: time.Unix(0, 0).UTC()}
	if err := Details(&b, DetailView{Object: o}); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	if strings.Contains(html, msg["edit_office"]) {
		t.Error("Edit button shown when office is disabled")
	}
	if !strings.Contains(html, msg["no_versions"]) {
		t.Error("expected no-versions message")
	}
}

func TestEditorPage(t *testing.T) {
	var b bytes.Buffer
	v := EditorView{Name: "memo.odt", ActionURL: "http://office.example/browser/h/cool.html?WOPISrc=http%3A%2F%2Fk%2Fwopi%2Ffiles%2Ff1", AccessToken: "tok-xyz"}
	if err := Editor(&b, v); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		`id="office_form"`, `target="office_frame"`, `id="office_frame"`,
		`action="http://office.example/browser/h/cool.html?WOPISrc=`, // engine URL
		`name="access_token" value="tok-xyz"`,                        // token in the POST body, not the URL
		`src="/editor.js"`,                                           // external submit script (#38: no inline)
		"memo.odt",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("editor page missing %q", want)
		}
	}
	// #38: the auto-submit must not be an inline <script> (no script body).
	if strings.Contains(html, "office_form').submit()") {
		t.Error("editor page still has an inline submit script (breaks a strict CSP)")
	}
}

func TestListingIsFragment(t *testing.T) {
	var b bytes.Buffer
	if err := Listing(&b, sampleView()); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	if strings.Contains(html, "<html") || strings.Contains(html, "<!doctype") {
		t.Error("listing fragment must not be a full document")
	}
	if !strings.Contains(html, "Designs") || !strings.Contains(html, "report.pdf") {
		t.Error("listing fragment missing content")
	}
}

func TestEmptyFolderMessage(t *testing.T) {
	var b bytes.Buffer
	if err := Listing(&b, View{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), msg["empty"]) {
		t.Error("empty folder should show the empty message")
	}
}

func TestNamesAreEscaped(t *testing.T) {
	var b bytes.Buffer
	v := View{Folders: []file.Folder{{ID: "x", Name: `<script>alert(1)</script>`}}}
	if err := Listing(&b, v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "<script>alert(1)</script>") {
		t.Error("folder name must be HTML-escaped")
	}
}

func TestInlineStylesheet(t *testing.T) {
	var b bytes.Buffer
	v := sampleView()
	v.Inline = true
	if err := Page(&b, v); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	if !strings.Contains(html, "<style>") || strings.Contains(html, `href="/style.css"`) {
		t.Error("inline view should embed <style>, not link the stylesheet")
	}
}
