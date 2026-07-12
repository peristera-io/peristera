package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/lib/pii"
)

// --- fakes ---

// fakeSvc is an in-memory api.Service: it records ownership and content so
// the HTTP round-trip can be asserted without the real chunk engine (which
// has its own tests).
type fakeSvc struct {
	objs     map[string]file.Object
	folders  map[string]file.Folder
	content  map[string][]byte
	owner    map[string]pii.Subject
	versions map[string][]file.Version // newest first, like the real service
	seq      int
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{objs: map[string]file.Object{}, folders: map[string]file.Folder{},
		content: map[string][]byte{}, owner: map[string]pii.Subject{},
		versions: map[string][]file.Version{}}
}

func ptrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (f *fakeSvc) Upload(_ context.Context, owner pii.Subject, folderID *string, name string, r io.Reader) (file.Object, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return file.Object{}, err // e.g. the MaxBytesReader limit, like the real engine
	}
	f.seq++
	id := string(rune('a' + f.seq))
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	o := file.Object{ID: id, Owner: owner, Name: name, Size: int64(len(b)), FolderID: folderID, Created: now, Updated: now}
	f.objs[id] = o
	f.content[id] = b
	f.owner[id] = owner
	f.versions[id] = []file.Version{{ID: id + "-v0", Ordinal: 0, Size: int64(len(b)), Created: now}}
	return o, nil
}

func (f *fakeSvc) WriteVersion(_ context.Context, caller pii.Subject, id string, r io.Reader) (string, error) {
	if err := f.authz(caller, id); err != nil {
		return "", err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err // e.g. the MaxBytesReader limit
	}
	o := f.objs[id]
	o.Size = int64(len(b))
	f.objs[id] = o
	f.content[id] = b
	next := f.versions[id][0].Ordinal + 1
	f.versions[id] = append([]file.Version{{ID: id + "-v" + string(rune('0'+next)), Ordinal: next, Size: int64(len(b))}}, f.versions[id]...)
	return string(rune('0' + next)), nil
}

func (f *fakeSvc) ListVersions(_ context.Context, caller pii.Subject, id string) ([]file.Version, error) {
	if err := f.authz(caller, id); err != nil {
		return nil, err
	}
	return f.versions[id], nil
}

func (f *fakeSvc) GetFolder(_ context.Context, caller pii.Subject, id string) (file.Folder, error) {
	if err := f.authzFolder(caller, id); err != nil {
		return file.Folder{}, err
	}
	return f.folders[id], nil
}

func (f *fakeSvc) DeleteFolderTree(_ context.Context, caller pii.Subject, id string) error {
	if err := f.authzFolder(caller, id); err != nil {
		return err
	}
	for oid, o := range f.objs {
		if ptrEq(o.FolderID, &id) {
			delete(f.objs, oid)
			delete(f.content, oid)
			delete(f.owner, oid)
		}
	}
	for cid, fol := range f.folders {
		if cid != id && ptrEq(fol.ParentID, &id) {
			if err := f.DeleteFolderTree(nil, caller, cid); err != nil {
				return err
			}
		}
	}
	delete(f.folders, id)
	delete(f.owner, id)
	return nil
}

// DownloadZip writes a marker plus the folder's direct file contents — the
// real zip walk is covered by the file-domain tests; here the HTTP contract
// (auth, headers, streaming) is what's under test.
func (f *fakeSvc) DownloadZip(_ context.Context, caller pii.Subject, folder *string, w io.Writer) error {
	if folder != nil {
		if err := f.authzFolder(caller, *folder); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte("ZIP:")); err != nil {
		return err
	}
	for id, o := range f.objs {
		if f.owner[id] == caller && ptrEq(o.FolderID, folder) {
			if _, err := w.Write(f.content[id]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *fakeSvc) authzFolder(caller pii.Subject, id string) error {
	if _, ok := f.folders[id]; !ok {
		return file.ErrNotFound
	}
	if f.owner[id] != caller {
		return file.ErrForbidden
	}
	return nil
}

func (f *fakeSvc) CreateFolder(_ context.Context, owner pii.Subject, parent *string, name string) (file.Folder, error) {
	f.seq++
	id := "F" + string(rune('a'+f.seq))
	fol := file.Folder{ID: id, Owner: owner, ParentID: parent, Name: name}
	f.folders[id] = fol
	f.owner[id] = owner
	return fol, nil
}

func (f *fakeSvc) ListChildren(_ context.Context, caller pii.Subject, folder *string) (file.Listing, error) {
	var l file.Listing
	for id, fol := range f.folders {
		if f.owner[id] == caller && ptrEq(fol.ParentID, folder) {
			l.Folders = append(l.Folders, fol)
		}
	}
	for id, o := range f.objs {
		if f.owner[id] == caller && ptrEq(o.FolderID, folder) {
			l.Files = append(l.Files, o)
		}
	}
	return l, nil
}

func (f *fakeSvc) RenameFile(_ context.Context, caller pii.Subject, id, name string) error {
	if err := f.authz(caller, id); err != nil {
		return err
	}
	o := f.objs[id]
	o.Name = name
	f.objs[id] = o
	return nil
}

func (f *fakeSvc) MoveFile(_ context.Context, caller pii.Subject, id string, dest *string) error {
	if err := f.authz(caller, id); err != nil {
		return err
	}
	o := f.objs[id]
	o.FolderID = dest
	f.objs[id] = o
	return nil
}

func (f *fakeSvc) RenameFolder(_ context.Context, caller pii.Subject, id, name string) error {
	if err := f.authzFolder(caller, id); err != nil {
		return err
	}
	fol := f.folders[id]
	fol.Name = name
	f.folders[id] = fol
	return nil
}

func (f *fakeSvc) MoveFolder(_ context.Context, caller pii.Subject, id string, dest *string) error {
	if err := f.authzFolder(caller, id); err != nil {
		return err
	}
	fol := f.folders[id]
	fol.ParentID = dest
	f.folders[id] = fol
	return nil
}

func (f *fakeSvc) DeleteFolder(_ context.Context, caller pii.Subject, id string) error {
	if err := f.authzFolder(caller, id); err != nil {
		return err
	}
	for _, o := range f.objs {
		if ptrEq(o.FolderID, &id) {
			return file.ErrNotEmpty
		}
	}
	for cid, fol := range f.folders {
		if cid != id && ptrEq(fol.ParentID, &id) {
			return file.ErrNotEmpty
		}
	}
	delete(f.folders, id)
	delete(f.owner, id)
	return nil
}

func (f *fakeSvc) authz(caller pii.Subject, id string) error {
	o, ok := f.objs[id]
	if !ok {
		return file.ErrNotFound
	}
	if f.owner[id] != caller {
		_ = o
		return file.ErrForbidden
	}
	return nil
}

func (f *fakeSvc) Get(_ context.Context, caller pii.Subject, id string) (file.Object, error) {
	if err := f.authz(caller, id); err != nil {
		return file.Object{}, err
	}
	return f.objs[id], nil
}

func (f *fakeSvc) Download(_ context.Context, caller pii.Subject, id string, w io.Writer) error {
	if err := f.authz(caller, id); err != nil {
		return err
	}
	_, err := w.Write(f.content[id])
	return err
}

func (f *fakeSvc) List(_ context.Context, caller pii.Subject) ([]file.Object, error) {
	var out []file.Object
	for id, o := range f.objs {
		if f.owner[id] == caller {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeSvc) Delete(_ context.Context, caller pii.Subject, id string) error {
	if err := f.authz(caller, id); err != nil {
		return err
	}
	delete(f.objs, id)
	delete(f.content, id)
	delete(f.owner, id)
	return nil
}

// fakeAuth maps a token to a subject; "" and unknown tokens are invalid.
type fakeAuth struct{ subs map[string]pii.Subject }

func (a fakeAuth) Subject(_ context.Context, token string) (pii.Subject, bool, error) {
	s, ok := a.subs[token]
	return s, ok, nil
}

func testHandler() (http.Handler, *fakeSvc, pii.Subject, pii.Subject) {
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}
	svc := newFakeSvc()
	auth := fakeAuth{subs: map[string]pii.Subject{"alice-tok": alice, "bob-tok": bob}}
	return New(svc, auth, 16).Routes(), svc, alice, bob // tiny cap exercises 413
}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- tests ---

func TestUnauthenticated(t *testing.T) {
	h, _, _, _ := testHandler()
	if rec := do(t, h, "GET", "/v1/files", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/files", "garbage", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: got %d, want 401", rec.Code)
	}
}

func TestUploadListDownloadDelete(t *testing.T) {
	h, _, _, _ := testHandler()

	rec := do(t, h, "POST", "/v1/files?name=notes.txt", "alice-tok", "hello kamara")
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: got %d, want 201 (%s)", rec.Code, rec.Body)
	}
	var created fileDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "notes.txt" || created.Size != 12 || created.Permalink != "/files/"+created.ID {
		t.Errorf("created = %+v", created)
	}

	// List shows it.
	rec = do(t, h, "GET", "/v1/files", "alice-tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: got %d", rec.Code)
	}
	var list struct{ Files []fileDTO }
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Files) != 1 || list.Files[0].ID != created.ID {
		t.Errorf("list = %+v", list.Files)
	}

	// Download returns the exact bytes with a filename.
	rec = do(t, h, "GET", "/v1/files/"+created.ID+"/content", "alice-tok", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello kamara" {
		t.Errorf("download: %d %q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "notes.txt") {
		t.Errorf("content-disposition = %q", cd)
	}

	// Delete, then it's gone.
	if rec = do(t, h, "DELETE", "/v1/files/"+created.ID, "alice-tok", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: got %d, want 204", rec.Code)
	}
	if rec = do(t, h, "GET", "/v1/files/"+created.ID, "alice-tok", ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete: got %d, want 404", rec.Code)
	}
}

func TestUploadRequiresName(t *testing.T) {
	h, _, _, _ := testHandler()
	if rec := do(t, h, "POST", "/v1/files", "alice-tok", "x"); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name: got %d, want 400", rec.Code)
	}
}

func TestPermissionsEnforced(t *testing.T) {
	h, _, _, _ := testHandler()
	// Alice uploads; Bob may neither read, download, nor delete it.
	rec := do(t, h, "POST", "/v1/files?name=secret.txt", "alice-tok", "top secret")
	var o fileDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &o)

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/v1/files/" + o.ID},
		{"GET", "/v1/files/" + o.ID + "/content"},
		{"DELETE", "/v1/files/" + o.ID},
	} {
		if rec := do(t, h, tc.method, tc.path, "bob-tok", ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as bob: got %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	// Bob's own list is empty (not Alice's file).
	rec = do(t, h, "GET", "/v1/files", "bob-tok", "")
	var list struct{ Files []fileDTO }
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Files) != 0 {
		t.Errorf("bob's list leaked: %+v", list.Files)
	}
}

func TestUploadTooLarge(t *testing.T) {
	h, _, _, _ := testHandler() // helper caps uploads at 16 bytes
	body := strings.Repeat("A", 100)
	if rec := do(t, h, "POST", "/v1/files?name=big.bin", "alice-tok", body); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload: got %d, want 413", rec.Code)
	}
}

func TestFolderAPIRoundTrip(t *testing.T) {
	h, _, _, _ := testHandler()

	// Create a folder; it appears in the root listing.
	rec := do(t, h, "POST", "/v1/folders?name=docs", "alice-tok", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create folder: %d %s", rec.Code, rec.Body)
	}
	var fol folderDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &fol)
	if fol.Name != "docs" || fol.Permalink != "/folders/"+fol.ID {
		t.Errorf("folder = %+v", fol)
	}
	rec = do(t, h, "GET", "/v1/folders", "alice-tok", "")
	var root struct {
		Folders []folderDTO
		Files   []fileDTO
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &root)
	if len(root.Folders) != 1 || root.Folders[0].ID != fol.ID {
		t.Errorf("root listing = %+v", root.Folders)
	}

	// Upload into the folder; it appears when listing that folder.
	rec = do(t, h, "POST", "/v1/files?name=f.txt&folder="+fol.ID, "alice-tok", "hi")
	var created fileDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Folder == nil || *created.Folder != fol.ID {
		t.Errorf("uploaded file folder = %v", created.Folder)
	}
	rec = do(t, h, "GET", "/v1/folders?parent="+fol.ID, "alice-tok", "")
	var in struct{ Files []fileDTO }
	_ = json.Unmarshal(rec.Body.Bytes(), &in)
	if len(in.Files) != 1 || in.Files[0].ID != created.ID {
		t.Errorf("folder listing = %+v", in.Files)
	}

	// Deleting a non-empty folder is a 409; after moving the file to root
	// it's a 204.
	if rec = do(t, h, "DELETE", "/v1/folders/"+fol.ID, "alice-tok", ""); rec.Code != http.StatusConflict {
		t.Errorf("delete non-empty folder: got %d, want 409", rec.Code)
	}
	if rec = do(t, h, "POST", "/v1/files/"+created.ID+"/move", "alice-tok", `{"folder":null}`); rec.Code != http.StatusNoContent {
		t.Errorf("move to root: got %d (%s)", rec.Code, rec.Body)
	}
	if rec = do(t, h, "DELETE", "/v1/folders/"+fol.ID, "alice-tok", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete emptied folder: got %d", rec.Code)
	}
	// Rename the file.
	if rec = do(t, h, "POST", "/v1/files/"+created.ID+"/rename", "alice-tok", `{"name":"renamed.txt"}`); rec.Code != http.StatusNoContent {
		t.Errorf("rename: got %d", rec.Code)
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	h, _, _, _ := testHandler()
	if rec := do(t, h, "GET", "/v1/files/nope", "alice-tok", ""); rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestUpdateContentAndVersions(t *testing.T) {
	h, _, _, _ := testHandler()
	rec := do(t, h, "POST", "/v1/files?name=notes.txt", "alice-tok", "v0")
	var created fileDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// PUT replaces the content and returns the updated metadata.
	rec = do(t, h, "PUT", "/v1/files/"+created.ID+"/content", "alice-tok", "second draft")
	if rec.Code != http.StatusOK {
		t.Fatalf("put content: got %d (%s)", rec.Code, rec.Body)
	}
	var updated fileDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.ID != created.ID || updated.Size != 12 {
		t.Errorf("updated = %+v", updated)
	}
	rec = do(t, h, "GET", "/v1/files/"+created.ID+"/content", "alice-tok", "")
	if rec.Body.String() != "second draft" {
		t.Errorf("content after put = %q", rec.Body.String())
	}

	// Versions list both revisions, newest first.
	rec = do(t, h, "GET", "/v1/files/"+created.ID+"/versions", "alice-tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("versions: got %d", rec.Code)
	}
	var vl struct{ Versions []versionDTO }
	_ = json.Unmarshal(rec.Body.Bytes(), &vl)
	if len(vl.Versions) != 2 || vl.Versions[0].Ordinal != 1 || vl.Versions[1].Ordinal != 0 {
		t.Errorf("versions = %+v, want ordinals [1 0]", vl.Versions)
	}

	// Not the owner: neither write nor read.
	if rec = do(t, h, "PUT", "/v1/files/"+created.ID+"/content", "bob-tok", "evil"); rec.Code != http.StatusForbidden {
		t.Errorf("put as bob: got %d, want 403", rec.Code)
	}
	if rec = do(t, h, "GET", "/v1/files/"+created.ID+"/versions", "bob-tok", ""); rec.Code != http.StatusForbidden {
		t.Errorf("versions as bob: got %d, want 403", rec.Code)
	}
}

func TestUpdateContentTooLarge(t *testing.T) {
	h, _, _, _ := testHandler() // helper caps uploads at 16 bytes
	rec := do(t, h, "POST", "/v1/files?name=small.txt", "alice-tok", "x")
	var created fileDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec = do(t, h, "PUT", "/v1/files/"+created.ID+"/content", "alice-tok", strings.Repeat("A", 100)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized put: got %d, want 413", rec.Code)
	}
}

func TestGetFolderAndZip(t *testing.T) {
	h, _, _, _ := testHandler()
	rec := do(t, h, "POST", "/v1/folders?name=docs", "alice-tok", "")
	var fol folderDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &fol)

	// Folder metadata.
	rec = do(t, h, "GET", "/v1/folders/"+fol.ID, "alice-tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get folder: got %d", rec.Code)
	}
	var got folderDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID != fol.ID || got.Name != "docs" {
		t.Errorf("folder = %+v", got)
	}

	// Zip download: streamed with the folder's name and the zip type.
	do(t, h, "POST", "/v1/files?name=in.txt&folder="+fol.ID, "alice-tok", "body")
	rec = do(t, h, "GET", "/v1/folders/"+fol.ID+"/zip", "alice-tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("zip: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("zip content-type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "docs.zip") {
		t.Errorf("zip content-disposition = %q", cd)
	}
	if !strings.Contains(rec.Body.String(), "body") {
		t.Errorf("zip body = %q", rec.Body.String())
	}

	// Not the owner: no metadata, no zip.
	if rec = do(t, h, "GET", "/v1/folders/"+fol.ID, "bob-tok", ""); rec.Code != http.StatusForbidden {
		t.Errorf("get folder as bob: got %d, want 403", rec.Code)
	}
	if rec = do(t, h, "GET", "/v1/folders/"+fol.ID+"/zip", "bob-tok", ""); rec.Code != http.StatusForbidden {
		t.Errorf("zip as bob: got %d, want 403", rec.Code)
	}
}

func TestDeleteFolderRecursive(t *testing.T) {
	h, svc, _, _ := testHandler()
	rec := do(t, h, "POST", "/v1/folders?name=docs", "alice-tok", "")
	var fol folderDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &fol)
	do(t, h, "POST", "/v1/files?name=in.txt&folder="+fol.ID, "alice-tok", "body")

	// Without recursive the old contract holds (409 on non-empty)…
	if rec = do(t, h, "DELETE", "/v1/folders/"+fol.ID, "alice-tok", ""); rec.Code != http.StatusConflict {
		t.Errorf("non-recursive delete: got %d, want 409", rec.Code)
	}
	// …with recursive=true the subtree goes.
	if rec = do(t, h, "DELETE", "/v1/folders/"+fol.ID+"?recursive=true", "alice-tok", ""); rec.Code != http.StatusNoContent {
		t.Errorf("recursive delete: got %d, want 204", rec.Code)
	}
	if len(svc.folders) != 0 || len(svc.objs) != 0 {
		t.Errorf("subtree survived: folders=%v objs=%v", svc.folders, svc.objs)
	}
}
