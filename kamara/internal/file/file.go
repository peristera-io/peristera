// Package file is Kamara's domain: files stored as content-defined,
// deduplicated, encrypted chunks, wired through the four conventions
// (personal-data metadata, OpenFGA authorization, audit, search) and the
// transactional-storage unit of work (root ADR-0015). The chunk engine
// (chunk/crypto/blob) does the byte work; this layer owns objects,
// versions, the manifest, ref-counting, and the conventions.
package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
	"github.com/peristera-io/peristera/kamara/internal/engine"
	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/id"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// Type is the app-namespaced object type (root ADR-0007/0010).
const Type = "kamara/file"

// FolderType is the app-namespaced folder type (Kamara ADR-0002).
const FolderType = "kamara/folder"

// Relation is the OpenFGA ownership relation (the tuple written on create).
const Relation = "owner"

// ParentRelation links a file/folder to its containing folder; AccessRelation
// is the computed access check (owner or inherited up the tree). Per-owner
// trees today, but the inheritance edges are real so sharing is later a
// tuple, not a model change (Kamara ADR-0002).
const (
	ParentRelation = "parent"
	AccessRelation = "can_access"
)

// ErrNotFound is returned when an object id doesn't exist.
var ErrNotFound = errors.New("file: not found")

// ErrForbidden is returned when the caller is not authorized on an object.
// The HTTP layer maps it to 403 (distinct from a 500 on a real failure).
var ErrForbidden = errors.New("file: not authorized")

// Object is a stored file (identity = UUIDv7, URLs carry it — ADR-0007).
// FolderID is its location: nil means the owner's root (Kamara ADR-0002).
type Object struct {
	ID       string
	Owner    pii.Subject
	Name     string
	Size     int64
	FolderID *string
	// ContentType is the file's MIME type, inferred from the name on upload
	// (#28). Empty means unknown; the HTTP layer falls back to
	// application/octet-stream.
	ContentType string
	Created     time.Time
	Updated     time.Time
}

// Permalink is the canonical URL (stable across moves — ADR-0007).
func (o Object) Permalink() string { return "/files/" + o.ID }

// Version is one revision of an object (the versions table). Save-back from
// the office engine appends a version (ADR-0018); the drawer lists them.
type Version struct {
	ID      string
	Ordinal int
	Size    int64
	Created time.Time
}

// Repo is the object/version/chunk metadata persistence port (Postgres in
// production, in-memory in tests). Chunk *bytes* live in the blob store,
// not here.
type Repo interface {
	InsertObject(ctx context.Context, o Object) error
	GetObject(ctx context.Context, id string) (Object, bool, error)
	ByIDs(ctx context.Context, ids []string) ([]Object, error)
	ByOwner(ctx context.Context, owner pii.Subject) ([]Object, error)
	DeleteObject(ctx context.Context, id string) error // cascades versions + manifest

	// InsertVersion records a version and its manifest, upserting each
	// chunk and incrementing its ref_count (cross-file/version dedup).
	InsertVersion(ctx context.Context, objectID, versionID string, ordinal int, size int64, refs []engine.ChunkRef) error
	// ManifestOf returns the ordered chunk refs of an object's latest version.
	ManifestOf(ctx context.Context, objectID string) ([]engine.ChunkRef, error)
	// MaxOrdinal returns the highest version ordinal for an object (ok=false
	// when it has no versions — treated as not-found). Save-back writes
	// ordinal+1 (ADR-0018).
	MaxOrdinal(ctx context.Context, objectID string) (ordinal int, ok bool, err error)
	// ListVersions returns an object's versions, newest first (for the drawer).
	ListVersions(ctx context.Context, objectID string) ([]Version, error)
	// SetObjectSize records a new latest-version size and bumps updated_at
	// (called on save-back so the object reflects its newest version).
	SetObjectSize(ctx context.Context, id string, size int64) error
	// ChunkHashesOf returns every chunk hash the object references.
	ChunkHashesOf(ctx context.Context, objectID string) ([]string, error)
	// DecRef decrements ref_count for each hash and returns those that
	// reached zero (now orphaned — their blobs can be reclaimed).
	DecRef(ctx context.Context, hashes []string) (orphans []string, err error)
	// DeleteChunks removes chunk rows (used for the collected orphans).
	DeleteChunks(ctx context.Context, hashes []string) error

	// SetObjectFolder moves a file to a folder (nil = root); SetObjectName
	// renames it. Both bump updated_at.
	SetObjectFolder(ctx context.Context, id string, folderID *string) error
	SetObjectName(ctx context.Context, id, name string) error

	// --- folders (Kamara ADR-0002) ---

	InsertFolder(ctx context.Context, f Folder) error
	GetFolder(ctx context.Context, id string) (Folder, bool, error)
	// FoldersInParent lists an owner's child folders of parent (nil = root).
	FoldersInParent(ctx context.Context, owner pii.Subject, parent *string) ([]Folder, error)
	// ObjectsInFolder lists an owner's files in folder (nil = root).
	ObjectsInFolder(ctx context.Context, owner pii.Subject, folder *string) ([]Object, error)
	// FolderHasChildren reports whether any folder or file references it
	// (deletion is empty-first).
	FolderHasChildren(ctx context.Context, id string) (bool, error)
	SetFolderParent(ctx context.Context, id string, parent *string) error
	SetFolderName(ctx context.Context, id, name string) error
	DeleteFolder(ctx context.Context, id string) error
	// FoldersByOwner returns all an owner's folders (export/erase scoping).
	FoldersByOwner(ctx context.Context, owner pii.Subject) ([]Folder, error)
}

// Authorizer is the subset of lib/authz the domain uses.
type Authorizer interface {
	Write(ctx context.Context, user pii.Subject, relation, object string) error
	Delete(ctx context.Context, user pii.Subject, relation, object string) error
	Check(ctx context.Context, user pii.Subject, relation, object string) (bool, error)
	ListObjects(ctx context.Context, user pii.Subject, relation, objectType string) ([]string, error)
	// Object-to-object containment edges (a file/folder's parent folder).
	WriteObjectTuple(ctx context.Context, user, relation, object string) error
	DeleteObjectTuple(ctx context.Context, user, relation, object string) error
}

// Stores bundles the same-database convention stores of one transaction.
type Stores struct {
	Objects Repo
	Audit   *audit.Emitter
	Search  *search.Feeder
}

// TxRunner runs a mutation's same-DB writes atomically and provides a
// non-transactional bundle for reads and export/erase (root ADR-0015).
type TxRunner interface {
	InTx(ctx context.Context, fn func(Stores) error) error
	Reader() Stores
}

// Service is the file domain.
type Service struct {
	tx     TxRunner
	authz  Authorizer
	blobs  blob.Store
	cipher *crypto.Cipher
	now    func() time.Time
}

// NewService builds the service and registers the file personal-data
// descriptor (ADR-0009). Pass pii.Default in production, a fresh registry
// in tests.
func NewService(reg *pii.Registry, txr TxRunner, az Authorizer, blobs blob.Store, cipher *crypto.Cipher) *Service {
	s := &Service{tx: txr, authz: az, blobs: blobs, cipher: cipher, now: time.Now}
	rd := txr.Reader()
	reg.Register(pii.Descriptor{
		Type:   Type,
		Fields: []string{"name"},
		Hooks:  &subjectData{repo: rd.Objects, blobs: blobs, search: rd.Search, authz: az},
	})
	return s
}

func obj(id string) string       { return Type + ":" + id }
func folderObj(id string) string { return FolderType + ":" + id }

// Upload stores a file. The chunk blobs are written durably FIRST (outside
// any transaction — Kamara ADR-0001/store discipline), then one
// transaction commits the object + version + manifest + chunk ref-counts +
// audit + search. A crash between the two orphans blobs (GC-collectable),
// never a dangling manifest. The OpenFGA tuple is the one out-of-tx step.
func (s *Service) Upload(ctx context.Context, owner pii.Subject, folderID *string, name string, r io.Reader) (Object, error) {
	if name == "" {
		return Object{}, fmt.Errorf("file: name required")
	}
	// A file placed in a folder requires access to that folder (root — nil —
	// is the owner's own, always allowed).
	if folderID != nil {
		if err := s.authorizeFolder(ctx, owner, *folderID); err != nil {
			return Object{}, err
		}
	}
	refs, total, err := engine.Ingest(ctx, r, s.cipher, s.blobs)
	if err != nil {
		return Object{}, err
	}
	now := s.now().UTC()
	o := Object{ID: id.V7(), Owner: owner, Name: name, Size: total, FolderID: folderID,
		ContentType: contentTypeOf(name), Created: now, Updated: now}
	verID := id.V7()
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Objects.InsertObject(ctx, o); err != nil {
			return err
		}
		if err := st.Objects.InsertVersion(ctx, o.ID, verID, 0, total, refs); err != nil {
			return err
		}
		if err := st.Audit.Emit(ctx, owner, "kamara.file.created",
			audit.Object{Type: Type, ID: o.ID, Permalink: o.Permalink()}, nil); err != nil {
			return err
		}
		return st.Search.Feed(ctx, search.Doc{
			ID: o.ID, Type: Type, Permalink: o.Permalink(), Owner: owner, Text: name})
	}); err != nil {
		return Object{}, err
	}
	if err := s.authz.Write(ctx, owner, Relation, obj(o.ID)); err != nil {
		return Object{}, fmt.Errorf("file: writing owner tuple: %w", err)
	}
	// Containment edge for inherited access (Kamara ADR-0002).
	if folderID != nil {
		if err := s.authz.WriteObjectTuple(ctx, folderObj(*folderID), ParentRelation, obj(o.ID)); err != nil {
			return Object{}, fmt.Errorf("file: writing parent tuple: %w", err)
		}
	}
	return o, nil
}

// Download streams the object's content to w after an authorization check.
func (s *Service) Download(ctx context.Context, caller pii.Subject, objectID string, w io.Writer) error {
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return err
	}
	refs, err := s.tx.Reader().Objects.ManifestOf(ctx, objectID)
	if err != nil {
		return err
	}
	return engine.Reassemble(ctx, refs, s.cipher, s.blobs, w)
}

// WriteVersion appends a new version of an existing object from r and returns
// the new version's ordinal (as a string — the WOPI X-WOPI-ItemVersion). Used
// by the office engine's save-back (ADR-0018): the file keeps its owner and
// identity; each save is a new revision, and the acting user is recorded in
// the audit event. Blob bytes are written durably first (like Upload), then
// one transaction commits the version + manifest + the object's new size.
func (s *Service) WriteVersion(ctx context.Context, caller pii.Subject, objectID string, r io.Reader) (string, error) {
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return "", err
	}
	refs, total, err := engine.Ingest(ctx, r, s.cipher, s.blobs)
	if err != nil {
		return "", err
	}
	verID := id.V7()
	var ordinal int
	if err := s.tx.InTx(ctx, func(st Stores) error {
		maxOrd, ok, err := st.Objects.MaxOrdinal(ctx, objectID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		ordinal = maxOrd + 1
		if err := st.Objects.InsertVersion(ctx, objectID, verID, ordinal, total, refs); err != nil {
			return err
		}
		if err := st.Objects.SetObjectSize(ctx, objectID, total); err != nil {
			return err
		}
		return st.Audit.Emit(ctx, caller, "kamara.file.version_written",
			audit.Object{Type: Type, ID: objectID, Permalink: "/files/" + objectID}, nil)
	}); err != nil {
		return "", err
	}
	return strconv.Itoa(ordinal), nil
}

// ListVersions returns an object's versions (newest first) after an
// authorization check — the data behind the details drawer's version list.
func (s *Service) ListVersions(ctx context.Context, caller pii.Subject, objectID string) ([]Version, error) {
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return nil, err
	}
	return s.tx.Reader().Objects.ListVersions(ctx, objectID)
}

// List returns the caller's files, permission-filtered through OpenFGA.
func (s *Service) List(ctx context.Context, caller pii.Subject) ([]Object, error) {
	ids, err := s.authz.ListObjects(ctx, caller, AccessRelation, Type)
	if err != nil {
		return nil, err
	}
	return s.tx.Reader().Objects.ByIDs(ctx, ids)
}

// Delete removes an object (after an authorization check): audit + object
// delete (cascading versions/manifest) + chunk ref-count decrement are one
// transaction; the now-orphaned chunk blobs and rows are reclaimed after,
// and the OpenFGA tuple is removed outside the transaction.
func (s *Service) Delete(ctx context.Context, caller pii.Subject, objectID string) error {
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return err
	}
	var orphans []string
	var folderID *string
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Audit.Emit(ctx, caller, "kamara.file.deleted",
			audit.Object{Type: Type, ID: objectID, Permalink: "/files/" + objectID}, nil); err != nil {
			return err
		}
		// Capture the folder before deletion so its containment tuple can be
		// cleaned up after commit.
		if o, ok, err := st.Objects.GetObject(ctx, objectID); err != nil {
			return err
		} else if ok {
			folderID = o.FolderID
		}
		hashes, err := st.Objects.ChunkHashesOf(ctx, objectID)
		if err != nil {
			return err
		}
		if err := st.Objects.DeleteObject(ctx, objectID); err != nil {
			return err
		}
		orphans, err = st.Objects.DecRef(ctx, hashes)
		if err != nil {
			return err
		}
		if err := st.Objects.DeleteChunks(ctx, orphans); err != nil {
			return err
		}
		return st.Search.Remove(ctx, objectID)
	}); err != nil {
		return err
	}
	// Reclaim orphaned blobs after the metadata is committed (best-effort;
	// a leftover blob is harmless and GC-collectable).
	for _, h := range orphans {
		_ = s.blobs.Delete(ctx, h)
	}
	if folderID != nil {
		// Containment tuple cleanup (best-effort; a dangling parent tuple to
		// a deleted file is harmless, reconciled like the owner-tuple seam).
		_ = s.authz.DeleteObjectTuple(ctx, folderObj(*folderID), ParentRelation, obj(objectID))
	}
	return s.authz.Delete(ctx, caller, Relation, obj(objectID))
}

// Get returns an object's metadata after an authorization check.
func (s *Service) Get(ctx context.Context, caller pii.Subject, objectID string) (Object, error) {
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return Object{}, err
	}
	o, ok, err := s.tx.Reader().Objects.GetObject(ctx, objectID)
	if err != nil {
		return Object{}, err
	}
	if !ok {
		return Object{}, ErrNotFound
	}
	return o, nil
}

// contentTypeOf infers a MIME type from a file name's extension (#28). It
// returns "" when unknown; the HTTP layer falls back to octet-stream. The
// charset suffix mime adds (e.g. "; charset=utf-8") is dropped — WOPI/office
// engines want the bare type.
func contentTypeOf(name string) string {
	ct := mime.TypeByExtension(filepath.Ext(name))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

func (s *Service) authorize(ctx context.Context, caller pii.Subject, objectID string) error {
	return s.authorizeAccess(ctx, caller, obj(objectID))
}

func (s *Service) authorizeFolder(ctx context.Context, caller pii.Subject, folderID string) error {
	return s.authorizeAccess(ctx, caller, folderObj(folderID))
}

// authorizeAccess checks the computed can_access relation (owner or
// inherited up the folder tree — Kamara ADR-0002) on a fully-qualified
// OpenFGA object.
func (s *Service) authorizeAccess(ctx context.Context, caller pii.Subject, object string) error {
	ok, err := s.authz.Check(ctx, caller, AccessRelation, object)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s on %s", ErrForbidden, caller, object)
	}
	return nil
}
