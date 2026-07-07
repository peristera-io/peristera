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
	"github.com/peristera-io/peristera/kamara/internal/wopi"
	"github.com/peristera-io/peristera/lib/pii"
)

// fakeWopiSvc is an in-memory WopiService.
type fakeWopiSvc struct {
	obj      file.Object
	content  []byte
	versions int
	written  []byte
}

func (f *fakeWopiSvc) Get(_ context.Context, _ pii.Subject, id string) (file.Object, error) {
	if id != f.obj.ID {
		return file.Object{}, file.ErrNotFound
	}
	return f.obj, nil
}
func (f *fakeWopiSvc) Download(_ context.Context, _ pii.Subject, _ string, w io.Writer) error {
	_, err := w.Write(f.content)
	return err
}
func (f *fakeWopiSvc) WriteVersion(_ context.Context, _ pii.Subject, _ string, r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.written = b
	f.content = b
	f.versions++
	return "1", nil
}

// fakeValidator resolves exactly one token to one session.
type fakeValidator struct {
	token string
	sess  wopi.Session
}

func (v *fakeValidator) Validate(_ context.Context, token string) (wopi.Session, error) {
	if token == "" || token != v.token {
		return wopi.Session{}, wopi.ErrInvalid
	}
	return v.sess, nil
}

func newWopiFixture(canWrite bool) (*WopiHandler, *fakeWopiSvc, string) {
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	svc := &fakeWopiSvc{
		obj: file.Object{ID: "file-1", Owner: alice, Name: "memo.odt", Size: 5,
			ContentType: "application/vnd.oasis.opendocument.text", Updated: time.Unix(1700000000, 0).UTC()},
		content: []byte("hello"),
	}
	val := &fakeValidator{token: "tok-abc", sess: wopi.Session{ObjectID: "file-1", Subject: alice, CanWrite: canWrite}}
	return NewWopi(svc, val, 0), svc, "tok-abc"
}

func TestWopiCheckFileInfo(t *testing.T) {
	h, _, tok := newWopiFixture(true)
	req := httptest.NewRequest("GET", "/wopi/files/file-1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var info map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["BaseFileName"] != "memo.odt" || info["UserCanWrite"] != true {
		t.Errorf("CheckFileInfo = %v", info)
	}
}

func TestWopiGetFile(t *testing.T) {
	h, _, tok := newWopiFixture(true)
	req := httptest.NewRequest("GET", "/wopi/files/file-1/contents?access_token="+tok, nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "opendocument") {
		t.Errorf("content type = %q (#28: should reflect stored type)", ct)
	}
}

func TestWopiPutFileWritesVersion(t *testing.T) {
	h, svc, tok := newWopiFixture(true)
	req := httptest.NewRequest("POST", "/wopi/files/file-1/contents", strings.NewReader("edited"))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-WOPI-Override", "PUT")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-WOPI-ItemVersion") != "1" {
		t.Errorf("X-WOPI-ItemVersion = %q", rec.Header().Get("X-WOPI-ItemVersion"))
	}
	if svc.versions != 1 || string(svc.written) != "edited" {
		t.Errorf("write not recorded: versions=%d written=%q", svc.versions, svc.written)
	}
}

func TestWopiPutFileDeniedWithoutWrite(t *testing.T) {
	h, svc, tok := newWopiFixture(false) // read-only session
	req := httptest.NewRequest("POST", "/wopi/files/file-1/contents", strings.NewReader("edited"))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if svc.versions != 0 {
		t.Error("read-only session wrote a version")
	}
}

func TestWopiRejectsBadToken(t *testing.T) {
	h, _, _ := newWopiFixture(true)
	req := httptest.NewRequest("GET", "/wopi/files/file-1", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// A token scoped to one file must not be usable against another (the path must
// match the session's object).
func TestWopiRejectsTokenForDifferentFile(t *testing.T) {
	h, _, tok := newWopiFixture(true)
	req := httptest.NewRequest("GET", "/wopi/files/file-2", nil) // token is for file-1
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("cross-file token status = %d, want 401", rec.Code)
	}
}
