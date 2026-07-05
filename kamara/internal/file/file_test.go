package file

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"slices"
	"testing"

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
	"github.com/peristera-io/peristera/kamara/internal/engine"
	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// --- in-memory Repo (metadata) ---

type memRepo struct {
	objs      map[string]Object
	manifests map[string][]engine.ChunkRef // objectID → refs
	refCount  map[string]int               // chunk hash → ref count
}

func newMemRepo() *memRepo {
	return &memRepo{objs: map[string]Object{}, manifests: map[string][]engine.ChunkRef{}, refCount: map[string]int{}}
}

func (m *memRepo) InsertObject(_ context.Context, o Object) error { m.objs[o.ID] = o; return nil }
func (m *memRepo) GetObject(_ context.Context, id string) (Object, bool, error) {
	o, ok := m.objs[id]
	return o, ok, nil
}
func (m *memRepo) ByIDs(_ context.Context, ids []string) ([]Object, error) {
	var out []Object
	for _, id := range ids {
		if o, ok := m.objs[id]; ok {
			out = append(out, o)
		}
	}
	return out, nil
}
func (m *memRepo) ByOwner(_ context.Context, o pii.Subject) ([]Object, error) {
	var out []Object
	for _, ob := range m.objs {
		if ob.Owner == o {
			out = append(out, ob)
		}
	}
	return out, nil
}
func (m *memRepo) DeleteObject(_ context.Context, id string) error {
	delete(m.objs, id)
	delete(m.manifests, id)
	return nil
}
func (m *memRepo) InsertVersion(_ context.Context, objectID, _ string, _ int, _ int64, refs []engine.ChunkRef) error {
	m.manifests[objectID] = refs
	for _, r := range refs {
		m.refCount[r.Hash]++
	}
	return nil
}
func (m *memRepo) ManifestOf(_ context.Context, objectID string) ([]engine.ChunkRef, error) {
	return m.manifests[objectID], nil
}
func (m *memRepo) ChunkHashesOf(_ context.Context, objectID string) ([]string, error) {
	var hs []string
	for _, r := range m.manifests[objectID] {
		hs = append(hs, r.Hash)
	}
	return hs, nil
}
func (m *memRepo) DecRef(_ context.Context, hashes []string) ([]string, error) {
	var orphans []string
	for _, h := range hashes {
		m.refCount[h]--
		if m.refCount[h] <= 0 {
			orphans = append(orphans, h)
			delete(m.refCount, h)
		}
	}
	return orphans, nil
}
func (m *memRepo) DeleteChunks(_ context.Context, _ []string) error { return nil }

var _ Repo = (*memRepo)(nil)

// --- in-memory authz ---

type memAuthz struct{ tuples map[string]bool }

func newMemAuthz() *memAuthz                  { return &memAuthz{tuples: map[string]bool{}} }
func k(u pii.Subject, rel, obj string) string { return obj + "|" + rel + "|" + u.String() }
func (a *memAuthz) Write(_ context.Context, u pii.Subject, rel, obj string) error {
	a.tuples[k(u, rel, obj)] = true
	return nil
}
func (a *memAuthz) Delete(_ context.Context, u pii.Subject, rel, obj string) error {
	delete(a.tuples, k(u, rel, obj))
	return nil
}
func (a *memAuthz) Check(_ context.Context, u pii.Subject, rel, obj string) (bool, error) {
	return a.tuples[k(u, rel, obj)], nil
}
func (a *memAuthz) ListObjects(_ context.Context, u pii.Subject, rel, typ string) ([]string, error) {
	var ids []string
	for key := range a.tuples {
		o, r, us, ok := split3(key)
		if ok && r == rel && us == u.String() && len(o) > len(typ) && o[:len(typ)+1] == typ+":" {
			ids = append(ids, o[len(typ)+1:])
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func split3(s string) (a, b, c string, ok bool) {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// --- tx + conventions ---

type memSink struct{ events []audit.Event }

func (m *memSink) Append(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

type memIndex struct{ docs map[string]search.Doc }

func (m *memIndex) Upsert(_ context.Context, d search.Doc) error { m.docs[d.ID] = d; return nil }
func (m *memIndex) Delete(_ context.Context, id string) error    { delete(m.docs, id); return nil }

type memTx struct{ stores Stores }

func (m *memTx) InTx(_ context.Context, fn func(Stores) error) error { return fn(m.stores) }
func (m *memTx) Reader() Stores                                      { return m.stores }

func newService(t *testing.T) (*Service, *pii.Registry, *memRepo, *memAuthz, *memSink, *memIndex, blob.Store) {
	t.Helper()
	repo := newMemRepo()
	az := newMemAuthz()
	sink := &memSink{}
	idx := &memIndex{docs: map[string]search.Doc{}}
	stores := Stores{
		Objects: repo,
		Audit:   audit.NewEmitter(sink, pii.NewInMemoryPseudonyms()),
		Search:  search.NewFeeder(idx),
	}
	fs, err := blob.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kkey := make([]byte, crypto.KeySize)
	crand.Read(kkey)
	ci, _ := crypto.New(kkey, "demo.example")
	reg := pii.NewRegistry()
	return NewService(reg, &memTx{stores}, az, fs, ci), reg, repo, az, sink, idx, fs
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, sink, idx, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	content := bytes.Repeat([]byte("kamara "), 500_000) // ~3.5 MB

	o, err := svc.Upload(ctx, alice, "notes.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if o.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", o.Size, len(content))
	}
	// Conventions fired.
	if len(sink.events) != 1 || sink.events[0].Action != "kamara.file.created" {
		t.Errorf("audit = %+v", sink.events)
	}
	if idx.docs[o.ID].Text != "notes.txt" {
		t.Error("search not fed")
	}
	// Download reassembles the exact bytes.
	var out bytes.Buffer
	if err := svc.Download(ctx, alice, o.ID, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Error("download mismatch")
	}
}

func TestListIsPermissionFiltered(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}

	a, _ := svc.Upload(ctx, alice, "a.txt", bytes.NewReader([]byte("a")))
	_, _ = svc.Upload(ctx, bob, "b.txt", bytes.NewReader([]byte("b")))

	got, err := svc.List(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("list leaked other owners: %+v", got)
	}
}

func TestUnauthorizedDownloadAndDelete(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}
	o, _ := svc.Upload(ctx, alice, "secret.txt", bytes.NewReader([]byte("secret")))

	if err := svc.Download(ctx, bob, o.ID, &bytes.Buffer{}); err == nil {
		t.Error("bob must not download alice's file")
	}
	if err := svc.Delete(ctx, bob, o.ID); err == nil {
		t.Error("bob must not delete alice's file")
	}
}

func TestDeleteReclaimsChunks(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, _, _, _, blobs := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	content := bytes.Repeat([]byte("x"), 2_000_000)
	o, _ := svc.Upload(ctx, alice, "f.bin", bytes.NewReader(content))

	hashes, _ := repo.ChunkHashesOf(ctx, o.ID)
	if len(hashes) == 0 {
		t.Fatal("no chunks recorded")
	}
	if err := svc.Delete(ctx, alice, o.ID); err != nil {
		t.Fatal(err)
	}
	// Chunk ref-counts gone → orphan blobs reclaimed.
	for _, h := range hashes {
		if ok, _ := blobs.Has(ctx, h); ok {
			t.Errorf("orphan blob %s not reclaimed after delete", h[:8])
		}
	}
	if _, ok, _ := repo.GetObject(ctx, o.ID); ok {
		t.Error("object still present after delete")
	}
}

func TestDedupKeepsSharedChunks(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, blobs := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	content := bytes.Repeat([]byte("shared"), 400_000)

	o1, _ := svc.Upload(ctx, alice, "one.txt", bytes.NewReader(content))
	o2, _ := svc.Upload(ctx, alice, "two.txt", bytes.NewReader(content))

	// Deleting one copy must NOT remove the shared chunks (ref_count > 0),
	// so the other copy still downloads.
	if err := svc.Delete(ctx, alice, o1.ID); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := svc.Download(ctx, alice, o2.ID, &out); err != nil {
		t.Fatalf("second copy broke after deleting the first: %v", err)
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Error("shared-chunk download mismatch")
	}
	_ = blobs
}

func TestExportAndErase(t *testing.T) {
	ctx := context.Background()
	svc, reg, _, _, _, idx, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	o, _ := svc.Upload(ctx, alice, "private.txt", bytes.NewReader([]byte("private")))

	out, err := reg.ExportSubject(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[Type]; !ok {
		t.Error("export missing the file")
	}
	if err := reg.EraseSubject(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.docs[o.ID]; ok {
		t.Error("search entry not removed on erase")
	}
	after, _ := reg.ExportSubject(ctx, alice)
	if _, ok := after[Type]; ok {
		t.Error("file still exportable after erase")
	}
}
