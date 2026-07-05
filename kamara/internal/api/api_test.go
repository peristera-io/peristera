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
	content map[string][]byte
	owner   map[string]pii.Subject
	seq     int
}

func newFakeSvc() *fakeSvc {
	return &fakeSvc{objs: map[string]file.Object{}, content: map[string][]byte{}, owner: map[string]pii.Subject{}}
}

func (f *fakeSvc) Upload(_ context.Context, owner pii.Subject, name string, r io.Reader) (file.Object, error) {
	b, _ := io.ReadAll(r)
	f.seq++
	id := string(rune('a' + f.seq))
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	o := file.Object{ID: id, Owner: owner, Name: name, Size: int64(len(b)), Created: now, Updated: now}
	f.objs[id] = o
	f.content[id] = b
	f.owner[id] = owner
	return o, nil
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
	return New(svc, auth).Routes(), svc, alice, bob
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

func TestGetMissingIsNotFound(t *testing.T) {
	h, _, _, _ := testHandler()
	if rec := do(t, h, "GET", "/v1/files/nope", "alice-tok", ""); rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}
