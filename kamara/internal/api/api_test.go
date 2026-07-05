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
	objs    map[string]file.Object
	folders map[string]file.Folder
	content map[string][]byte
	owner   map[string]pii.Subject
	seq     int
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{objs: map[string]file.Object{}, folders: map[string]file.Folder{},
		content: map[string][]byte{}, owner: map[string]pii.Subject{}}
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
	return o, nil
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
