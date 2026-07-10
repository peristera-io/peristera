package file

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/peristera-io/peristera/lib/pii"
)

func readZip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(content)
	}
	return out
}

func TestDownloadZipSubtree(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	docs, _ := svc.CreateFolder(ctx, alice, nil, "docs")
	docsID := docs.ID
	sub, _ := svc.CreateFolder(ctx, alice, &docsID, "sub")
	subID := sub.ID
	_, _ = svc.CreateFolder(ctx, alice, &docsID, "empty") // must survive as a dir entry
	if _, err := svc.Upload(ctx, alice, &docsID, "a.txt", strings.NewReader("alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(ctx, alice, &subID, "b.txt", strings.NewReader("beta")); err != nil {
		t.Fatal(err)
	}
	// Root-level file must NOT appear in the docs zip.
	if _, err := svc.Upload(ctx, alice, nil, "outside.txt", strings.NewReader("nope")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := svc.DownloadZip(ctx, alice, &docsID, &buf); err != nil {
		t.Fatal(err)
	}
	entries := readZip(t, buf.Bytes())
	if entries["a.txt"] != "alpha" {
		t.Errorf("a.txt = %q", entries["a.txt"])
	}
	if entries["sub/b.txt"] != "beta" {
		t.Errorf("sub/b.txt = %q", entries["sub/b.txt"])
	}
	if _, ok := entries["empty/"]; !ok {
		t.Errorf("empty folder lost; entries = %v", keys(entries))
	}
	for name := range entries {
		if strings.Contains(name, "outside") {
			t.Errorf("zip leaked a file outside the subtree: %s", name)
		}
	}

	// The root zip (nil folder) contains everything.
	buf.Reset()
	if err := svc.DownloadZip(ctx, alice, nil, &buf); err != nil {
		t.Fatal(err)
	}
	all := readZip(t, buf.Bytes())
	if all["outside.txt"] != "nope" || all["docs/a.txt"] != "alpha" || all["docs/sub/b.txt"] != "beta" {
		t.Errorf("root zip entries = %v", keys(all))
	}
}

func TestDownloadZipEntryNames(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	// Sibling name collision: the second entry gets a suffix, none shadowed.
	_, _ = svc.Upload(ctx, alice, nil, "dup.txt", strings.NewReader("one"))
	_, _ = svc.Upload(ctx, alice, nil, "dup.txt", strings.NewReader("two"))
	// A separator in a display name must not nest (or, extracted, escape).
	_, _ = svc.Upload(ctx, alice, nil, "../evil/name.txt", strings.NewReader("sly"))

	var buf bytes.Buffer
	if err := svc.DownloadZip(ctx, alice, nil, &buf); err != nil {
		t.Fatal(err)
	}
	entries := readZip(t, buf.Bytes())
	got := map[string]bool{}
	for name := range entries {
		got[name] = true
		if strings.Contains(name, "/") && !strings.HasSuffix(name, "/") {
			t.Errorf("flattening failed, entry has a path: %q", name)
		}
	}
	if !got["dup.txt"] || !got["dup (2).txt"] {
		t.Errorf("collision suffix missing: %v", keys(entries))
	}
	if len(entries) != 3 {
		t.Errorf("want 3 entries, got %v", keys(entries))
	}
}

func TestDownloadZipUnauthorized(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}
	f, _ := svc.CreateFolder(ctx, alice, nil, "private")
	fid := f.ID

	if err := svc.DownloadZip(ctx, bob, &fid, io.Discard); !errors.Is(err, ErrForbidden) {
		t.Errorf("bob zipped alice's folder: err = %v, want ErrForbidden", err)
	}
	// Bob's root zip contains only bob's things (here: nothing).
	var buf bytes.Buffer
	if err := svc.DownloadZip(ctx, bob, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if entries := readZip(t, buf.Bytes()); len(entries) != 0 {
		t.Errorf("bob's root zip leaked: %v", keys(entries))
	}
}

func TestDeleteFolderTree(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, _, sink, idx, blobs := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	docs, _ := svc.CreateFolder(ctx, alice, nil, "docs")
	docsID := docs.ID
	sub, _ := svc.CreateFolder(ctx, alice, &docsID, "sub")
	subID := sub.ID
	inner, _ := svc.Upload(ctx, alice, &subID, "inner.txt", bytes.NewReader(bytes.Repeat([]byte("x"), 2_000_000)))
	direct, _ := svc.Upload(ctx, alice, &docsID, "direct.txt", strings.NewReader("d"))
	keep, _ := svc.Upload(ctx, alice, nil, "keep.txt", strings.NewReader("k"))
	hashes, _ := repo.ChunkHashesOf(ctx, inner.ID)

	if err := svc.DeleteFolderTree(ctx, alice, docsID); err != nil {
		t.Fatal(err)
	}
	// The whole subtree is gone — folders, files, search entries, blobs.
	for _, id := range []string{docsID, subID} {
		if _, ok, _ := repo.GetFolder(ctx, id); ok {
			t.Errorf("folder %s survived", id)
		}
	}
	for _, id := range []string{inner.ID, direct.ID} {
		if _, ok, _ := repo.GetObject(ctx, id); ok {
			t.Errorf("object %s survived", id)
		}
		if _, ok := idx.docs[id]; ok {
			t.Errorf("search entry %s survived", id)
		}
	}
	for _, h := range hashes {
		if ok, _ := blobs.Has(ctx, h); ok {
			t.Errorf("orphan blob %s not reclaimed", h[:8])
		}
	}
	// The root file is untouched.
	if err := svc.Download(ctx, alice, keep.ID, io.Discard); err != nil {
		t.Errorf("unrelated file broke: %v", err)
	}
	// Every deletion was audited (2 folders + 2 files).
	var files, folders int
	for _, e := range sink.events {
		switch e.Action {
		case "kamara.file.deleted":
			files++
		case "kamara.folder.deleted":
			folders++
		}
	}
	if files != 2 || folders != 2 {
		t.Errorf("audit: %d file / %d folder deletions, want 2/2", files, folders)
	}
}

func TestDeleteFolderTreeUnauthorized(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}
	f, _ := svc.CreateFolder(ctx, alice, nil, "private")
	fid := f.ID
	o, _ := svc.Upload(ctx, alice, &fid, "s.txt", strings.NewReader("s"))

	if err := svc.DeleteFolderTree(ctx, bob, fid); !errors.Is(err, ErrForbidden) {
		t.Errorf("bob deleted alice's tree: err = %v", err)
	}
	if _, ok, _ := repo.GetObject(ctx, o.ID); !ok {
		t.Error("file vanished after a forbidden delete")
	}
}

func TestWriteVersionAt(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	o, _ := svc.Upload(ctx, alice, nil, "notes.txt", strings.NewReader("v0"))

	// A save based on the current version succeeds.
	ver, err := svc.WriteVersionAt(ctx, alice, o.ID, 0, strings.NewReader("v1"))
	if err != nil || ver != "1" {
		t.Fatalf("save at base 0: ver=%q err=%v", ver, err)
	}
	// A save from a stale tab (still base 0) is refused, and the newer
	// content survives.
	if _, err := svc.WriteVersionAt(ctx, alice, o.ID, 0, strings.NewReader("stale")); !errors.Is(err, ErrModified) {
		t.Fatalf("stale save: err = %v, want ErrModified", err)
	}
	var out bytes.Buffer
	_ = svc.Download(ctx, alice, o.ID, &out)
	if out.String() != "v1" {
		t.Errorf("content after refused save = %q, want %q", out.String(), "v1")
	}
	// Saving at the new base works.
	if _, err := svc.WriteVersionAt(ctx, alice, o.ID, 1, strings.NewReader("v2")); err != nil {
		t.Errorf("save at base 1: %v", err)
	}
}

func TestEmptyFileCreateAndEdit(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	// "New text file": an empty upload, then the first editor save.
	o, err := svc.Upload(ctx, alice, nil, "new.txt", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if o.Size != 0 || !o.TextEditable() {
		t.Errorf("empty file: size=%d editable=%v", o.Size, o.TextEditable())
	}
	var out bytes.Buffer
	if err := svc.Download(ctx, alice, o.ID, &out); err != nil || out.Len() != 0 {
		t.Errorf("empty download: len=%d err=%v", out.Len(), err)
	}
	if _, err := svc.WriteVersionAt(ctx, alice, o.ID, 0, strings.NewReader("hello")); err != nil {
		t.Fatalf("first save: %v", err)
	}
	out.Reset()
	_ = svc.Download(ctx, alice, o.ID, &out)
	if out.String() != "hello" {
		t.Errorf("content = %q", out.String())
	}
}

func TestEditabilityHelpers(t *testing.T) {
	for _, tc := range []struct {
		o        Object
		editable bool
		preview  bool
	}{
		{Object{Name: "a.txt", ContentType: "text/plain", Size: 10}, true, false},
		{Object{Name: "a.json", ContentType: "application/json", Size: 10}, true, false},
		{Object{Name: "noext", ContentType: "", Size: 10}, true, false},
		{Object{Name: "big.txt", ContentType: "text/plain", Size: MaxTextEditBytes + 1}, false, false},
		{Object{Name: "a.pdf", ContentType: "application/pdf", Size: 10}, false, false},
		{Object{Name: "a.png", ContentType: "image/png", Size: 10}, false, true},
		// SVG: neither a textarea target nor safe to render inline.
		{Object{Name: "a.svg", ContentType: "image/svg+xml", Size: 10}, false, false},
	} {
		if got := tc.o.TextEditable(); got != tc.editable {
			t.Errorf("%s: TextEditable = %v, want %v", tc.o.Name, got, tc.editable)
		}
		if got := tc.o.Previewable(); got != tc.preview {
			t.Errorf("%s: Previewable = %v, want %v", tc.o.Name, got, tc.preview)
		}
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
