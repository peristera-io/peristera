package file

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"errors"
	"slices"
	"strings"
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
	folders   map[string]Folder
	manifests map[string][]engine.ChunkRef // objectID → refs
	refCount  map[string]int               // chunk hash → ref count
}

func newMemRepo() *memRepo {
	return &memRepo{objs: map[string]Object{}, folders: map[string]Folder{}, manifests: map[string][]engine.ChunkRef{}, refCount: map[string]int{}}
}

func ptrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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

func (m *memRepo) SetObjectFolder(_ context.Context, id string, folder *string) error {
	o := m.objs[id]
	o.FolderID = folder
	m.objs[id] = o
	return nil
}
func (m *memRepo) SetObjectName(_ context.Context, id, name string) error {
	o := m.objs[id]
	o.Name = name
	m.objs[id] = o
	return nil
}
func (m *memRepo) InsertFolder(_ context.Context, f Folder) error { m.folders[f.ID] = f; return nil }
func (m *memRepo) GetFolder(_ context.Context, id string) (Folder, bool, error) {
	f, ok := m.folders[id]
	return f, ok, nil
}
func (m *memRepo) FoldersInParent(_ context.Context, owner pii.Subject, parent *string) ([]Folder, error) {
	var out []Folder
	for _, f := range m.folders {
		if f.Owner == owner && ptrEq(f.ParentID, parent) {
			out = append(out, f)
		}
	}
	return out, nil
}
func (m *memRepo) ObjectsInFolder(_ context.Context, owner pii.Subject, folder *string) ([]Object, error) {
	var out []Object
	for _, o := range m.objs {
		if o.Owner == owner && ptrEq(o.FolderID, folder) {
			out = append(out, o)
		}
	}
	return out, nil
}
func (m *memRepo) FolderHasChildren(_ context.Context, id string) (bool, error) {
	for _, f := range m.folders {
		if f.ParentID != nil && *f.ParentID == id {
			return true, nil
		}
	}
	for _, o := range m.objs {
		if o.FolderID != nil && *o.FolderID == id {
			return true, nil
		}
	}
	return false, nil
}
func (m *memRepo) SetFolderParent(_ context.Context, id string, parent *string) error {
	f := m.folders[id]
	f.ParentID = parent
	m.folders[id] = f
	return nil
}
func (m *memRepo) SetFolderName(_ context.Context, id, name string) error {
	f := m.folders[id]
	f.Name = name
	m.folders[id] = f
	return nil
}
func (m *memRepo) DeleteFolder(_ context.Context, id string) error { delete(m.folders, id); return nil }
func (m *memRepo) FoldersByOwner(_ context.Context, owner pii.Subject) ([]Folder, error) {
	var out []Folder
	for _, f := range m.folders {
		if f.Owner == owner {
			out = append(out, f)
		}
	}
	return out, nil
}

var _ Repo = (*memRepo)(nil)

// --- in-memory authz (models the OpenFGA can_access = owner or via parent) ---

type memAuthz struct {
	owners  map[string]map[string]bool // object → set of owner subject strings
	parents map[string]string          // child object → parent object
}

func newMemAuthz() *memAuthz {
	return &memAuthz{owners: map[string]map[string]bool{}, parents: map[string]string{}}
}

func (a *memAuthz) Write(_ context.Context, u pii.Subject, _, obj string) error {
	if a.owners[obj] == nil {
		a.owners[obj] = map[string]bool{}
	}
	a.owners[obj][u.OpenFGAObject()] = true
	return nil
}
func (a *memAuthz) Delete(_ context.Context, u pii.Subject, _, obj string) error {
	if s := a.owners[obj]; s != nil {
		delete(s, u.OpenFGAObject())
	}
	return nil
}
func (a *memAuthz) WriteObjectTuple(_ context.Context, parent, _, child string) error {
	a.parents[child] = parent
	return nil
}
func (a *memAuthz) DeleteObjectTuple(_ context.Context, parent, _, child string) error {
	if a.parents[child] == parent {
		delete(a.parents, child)
	}
	return nil
}
func (a *memAuthz) canAccess(userStr, obj string) bool {
	if a.owners[obj][userStr] {
		return true
	}
	if p, ok := a.parents[obj]; ok {
		return a.canAccess(userStr, p)
	}
	return false
}
func (a *memAuthz) Check(_ context.Context, u pii.Subject, rel, obj string) (bool, error) {
	us := u.OpenFGAObject()
	if rel == Relation { // direct owner
		return a.owners[obj][us], nil
	}
	return a.canAccess(us, obj), nil // AccessRelation (can_access)
}
func (a *memAuthz) ListObjects(_ context.Context, u pii.Subject, rel, typ string) ([]string, error) {
	us := u.OpenFGAObject()
	prefix := typ + ":"
	seen := map[string]bool{}
	var ids []string
	consider := func(obj string) {
		if seen[obj] || !strings.HasPrefix(obj, prefix) {
			return
		}
		seen[obj] = true
		ok := a.canAccess(us, obj)
		if rel == Relation {
			ok = a.owners[obj][us]
		}
		if ok {
			ids = append(ids, obj[len(prefix):])
		}
	}
	for obj := range a.owners {
		consider(obj)
	}
	for obj := range a.parents {
		consider(obj)
	}
	slices.Sort(ids)
	return ids, nil
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

	o, err := svc.Upload(ctx, alice, nil, "notes.txt", bytes.NewReader(content))
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

	a, _ := svc.Upload(ctx, alice, nil, "a.txt", bytes.NewReader([]byte("a")))
	_, _ = svc.Upload(ctx, bob, nil, "b.txt", bytes.NewReader([]byte("b")))

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
	o, _ := svc.Upload(ctx, alice, nil, "secret.txt", bytes.NewReader([]byte("secret")))

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
	o, _ := svc.Upload(ctx, alice, nil, "f.bin", bytes.NewReader(content))

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

	o1, _ := svc.Upload(ctx, alice, nil, "one.txt", bytes.NewReader(content))
	o2, _ := svc.Upload(ctx, alice, nil, "two.txt", bytes.NewReader(content))

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

func TestFolderCreateUploadAndList(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	f, err := svc.CreateFolder(ctx, alice, nil, "docs")
	if err != nil {
		t.Fatal(err)
	}
	fID := f.ID
	o, err := svc.Upload(ctx, alice, &fID, "in-folder.txt", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	// Root shows the folder, not the file (the file lives in the folder).
	root, _ := svc.ListChildren(ctx, alice, nil)
	if len(root.Folders) != 1 || root.Folders[0].ID != f.ID {
		t.Errorf("root folders = %+v", root.Folders)
	}
	if len(root.Files) != 0 {
		t.Errorf("root should have no files, got %+v", root.Files)
	}
	// The folder shows the file.
	in, _ := svc.ListChildren(ctx, alice, &fID)
	if len(in.Files) != 1 || in.Files[0].ID != o.ID {
		t.Errorf("folder files = %+v", in.Files)
	}
}

func TestFolderAccessIsInherited(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}

	f, _ := svc.CreateFolder(ctx, alice, nil, "private")
	fID := f.ID
	o, _ := svc.Upload(ctx, alice, &fID, "secret.txt", bytes.NewReader([]byte("s")))

	// Owner reaches the file via folder inheritance.
	if err := svc.Download(ctx, alice, o.ID, &bytes.Buffer{}); err != nil {
		t.Fatalf("owner denied own file: %v", err)
	}
	// A stranger reaches neither the folder nor the inherited file.
	if _, err := svc.ListChildren(ctx, bob, &fID); err == nil {
		t.Error("bob listed alice's folder")
	}
	if err := svc.Download(ctx, bob, o.ID, &bytes.Buffer{}); err == nil {
		t.Error("bob downloaded a file in alice's folder")
	}
}

func TestMoveFileBetweenFolders(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	a, _ := svc.CreateFolder(ctx, alice, nil, "a")
	b, _ := svc.CreateFolder(ctx, alice, nil, "b")
	aID, bID := a.ID, b.ID
	o, _ := svc.Upload(ctx, alice, &aID, "f.txt", bytes.NewReader([]byte("x")))

	if err := svc.MoveFile(ctx, alice, o.ID, &bID); err != nil {
		t.Fatal(err)
	}
	inA, _ := svc.ListChildren(ctx, alice, &aID)
	inB, _ := svc.ListChildren(ctx, alice, &bID)
	if len(inA.Files) != 0 || len(inB.Files) != 1 || inB.Files[0].ID != o.ID {
		t.Errorf("after move: A=%+v B=%+v", inA.Files, inB.Files)
	}
	// Access still resolves (inherited via the new parent).
	if err := svc.Download(ctx, alice, o.ID, &bytes.Buffer{}); err != nil {
		t.Errorf("owner lost access after move: %v", err)
	}
}

func TestFolderDeleteIsEmptyFirst(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	f, _ := svc.CreateFolder(ctx, alice, nil, "d")
	fID := f.ID
	o, _ := svc.Upload(ctx, alice, &fID, "f.txt", bytes.NewReader([]byte("x")))

	if err := svc.DeleteFolder(ctx, alice, f.ID); !errorsIs(err, ErrNotEmpty) {
		t.Errorf("delete non-empty folder: got %v, want ErrNotEmpty", err)
	}
	if err := svc.Delete(ctx, alice, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteFolder(ctx, alice, f.ID); err != nil {
		t.Errorf("delete emptied folder: %v", err)
	}
}

func TestFolderMoveCyclePrevented(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	a, _ := svc.CreateFolder(ctx, alice, nil, "a")
	aID := a.ID
	b, _ := svc.CreateFolder(ctx, alice, &aID, "b") // b under a
	bID := b.ID

	// Moving a under its own child b is a cycle.
	if err := svc.MoveFolder(ctx, alice, a.ID, &bID); !errorsIs(err, ErrCycle) {
		t.Errorf("cycle move: got %v, want ErrCycle", err)
	}
}

func errorsIs(err, target error) bool { return err != nil && errors.Is(err, target) }

func TestExportAndErase(t *testing.T) {
	ctx := context.Background()
	svc, reg, repo, _, _, idx, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	// A folder with a file inside, plus a root file — erase must reach all.
	f, _ := svc.CreateFolder(ctx, alice, nil, "folder")
	fID := f.ID
	inner, _ := svc.Upload(ctx, alice, &fID, "inner.txt", bytes.NewReader([]byte("in")))
	o, _ := svc.Upload(ctx, alice, nil, "private.txt", bytes.NewReader([]byte("private")))

	out, err := reg.ExportSubject(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[Type]; !ok {
		t.Error("export missing the file domain")
	}
	if err := reg.EraseSubject(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.docs[o.ID]; ok {
		t.Error("search entry not removed on erase")
	}
	if _, ok := idx.docs[f.ID]; ok {
		t.Error("folder search entry not removed on erase")
	}
	// Rows gone: object, inner object, folder.
	for _, id := range []string{o.ID, inner.ID} {
		if _, ok, _ := repo.GetObject(ctx, id); ok {
			t.Errorf("object %s survived erase", id)
		}
	}
	if _, ok, _ := repo.GetFolder(ctx, f.ID); ok {
		t.Error("folder survived erase")
	}
	after, _ := reg.ExportSubject(ctx, alice)
	if _, ok := after[Type]; ok {
		t.Error("still exportable after erase")
	}
}
