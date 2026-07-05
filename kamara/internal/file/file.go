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

// Relation is the OpenFGA ownership relation.
const Relation = "owner"

// ErrNotFound is returned when an object id doesn't exist.
var ErrNotFound = errors.New("file: not found")

// ErrForbidden is returned when the caller is not authorized on an object.
// The HTTP layer maps it to 403 (distinct from a 500 on a real failure).
var ErrForbidden = errors.New("file: not authorized")

// Object is a stored file (identity = UUIDv7, URLs carry it — ADR-0007).
type Object struct {
	ID      string
	Owner   pii.Subject
	Name    string
	Size    int64
	Created time.Time
	Updated time.Time
}

// Permalink is the canonical URL.
func (o Object) Permalink() string { return "/files/" + o.ID }

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
	// ManifestOf returns the ordered chunk refs of an object's current
	// (only, for M4a) version.
	ManifestOf(ctx context.Context, objectID string) ([]engine.ChunkRef, error)
	// ChunkHashesOf returns every chunk hash the object references.
	ChunkHashesOf(ctx context.Context, objectID string) ([]string, error)
	// DecRef decrements ref_count for each hash and returns those that
	// reached zero (now orphaned — their blobs can be reclaimed).
	DecRef(ctx context.Context, hashes []string) (orphans []string, err error)
	// DeleteChunks removes chunk rows (used for the collected orphans).
	DeleteChunks(ctx context.Context, hashes []string) error
}

// Authorizer is the subset of lib/authz the domain uses.
type Authorizer interface {
	Write(ctx context.Context, user pii.Subject, relation, object string) error
	Delete(ctx context.Context, user pii.Subject, relation, object string) error
	Check(ctx context.Context, user pii.Subject, relation, object string) (bool, error)
	ListObjects(ctx context.Context, user pii.Subject, relation, objectType string) ([]string, error)
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

func obj(id string) string { return Type + ":" + id }

// Upload stores a file. The chunk blobs are written durably FIRST (outside
// any transaction — Kamara ADR-0001/store discipline), then one
// transaction commits the object + version + manifest + chunk ref-counts +
// audit + search. A crash between the two orphans blobs (GC-collectable),
// never a dangling manifest. The OpenFGA tuple is the one out-of-tx step.
func (s *Service) Upload(ctx context.Context, owner pii.Subject, name string, r io.Reader) (Object, error) {
	if name == "" {
		return Object{}, fmt.Errorf("file: name required")
	}
	refs, total, err := engine.Ingest(ctx, r, s.cipher, s.blobs)
	if err != nil {
		return Object{}, err
	}
	now := s.now().UTC()
	o := Object{ID: id.V7(), Owner: owner, Name: name, Size: total, Created: now, Updated: now}
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

// List returns the caller's files, permission-filtered through OpenFGA.
func (s *Service) List(ctx context.Context, caller pii.Subject) ([]Object, error) {
	ids, err := s.authz.ListObjects(ctx, caller, Relation, Type)
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
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Audit.Emit(ctx, caller, "kamara.file.deleted",
			audit.Object{Type: Type, ID: objectID, Permalink: "/files/" + objectID}, nil); err != nil {
			return err
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

func (s *Service) authorize(ctx context.Context, caller pii.Subject, objectID string) error {
	ok, err := s.authz.Check(ctx, caller, Relation, obj(objectID))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s on %s", ErrForbidden, caller, objectID)
	}
	return nil
}
