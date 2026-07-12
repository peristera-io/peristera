package file

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/id"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// ErrNotEmpty is returned when deleting a folder that still holds children;
// ErrCycle when a move would put a folder inside its own subtree.
var (
	ErrNotEmpty = errors.New("file: folder not empty")
	ErrCycle    = errors.New("file: move would create a cycle")
)

// Folder is a node in the hierarchy (identity = UUIDv7, URLs carry it —
// ADR-0007). ParentID is nil for a root-level folder (Kamara ADR-0002).
type Folder struct {
	ID       string
	Owner    pii.Subject
	ParentID *string
	Name     string
	Created  time.Time
	Updated  time.Time
}

// Permalink is the canonical URL (stable across moves).
func (f Folder) Permalink() string { return "/folders/" + f.ID }

// Listing is the contents of one folder (or the root): its child folders
// and files.
type Listing struct {
	Folders []Folder
	Files   []Object
}

// CreateFolder creates a folder under parent (nil = the owner's root). Same
// unit-of-work discipline as Upload: row+audit+search in one transaction,
// the OpenFGA tuples after commit (root ADR-0015).
func (s *Service) CreateFolder(ctx context.Context, owner pii.Subject, parent *string, name string) (Folder, error) {
	if name == "" {
		return Folder{}, fmt.Errorf("file: folder name required")
	}
	if parent != nil {
		if err := s.authorizeFolder(ctx, owner, *parent); err != nil {
			return Folder{}, err
		}
	}
	now := s.now().UTC()
	f := Folder{ID: id.V7(), Owner: owner, ParentID: parent, Name: name, Created: now, Updated: now}
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Objects.InsertFolder(ctx, f); err != nil {
			return err
		}
		if err := st.Audit.Emit(ctx, owner, "kamara.folder.created",
			audit.Object{Type: FolderType, ID: f.ID, Permalink: f.Permalink()}, nil); err != nil {
			return err
		}
		return st.Search.Feed(ctx, search.Doc{
			ID: f.ID, Type: FolderType, Permalink: f.Permalink(), Owner: owner, Text: name})
	}); err != nil {
		return Folder{}, err
	}
	if err := s.authz.Write(ctx, owner, Relation, folderObj(f.ID)); err != nil {
		return Folder{}, fmt.Errorf("folder: writing owner tuple: %w", err)
	}
	if parent != nil {
		if err := s.authz.WriteObjectTuple(ctx, folderObj(*parent), ParentRelation, folderObj(f.ID)); err != nil {
			return Folder{}, fmt.Errorf("folder: writing parent tuple: %w", err)
		}
	}
	return f, nil
}

// GetFolder returns a folder's metadata after an access check.
func (s *Service) GetFolder(ctx context.Context, caller pii.Subject, folderID string) (Folder, error) {
	if err := s.authorizeFolder(ctx, caller, folderID); err != nil {
		return Folder{}, err
	}
	f, ok, err := s.tx.Reader().Objects.GetFolder(ctx, folderID)
	if err != nil {
		return Folder{}, err
	}
	if !ok {
		return Folder{}, ErrNotFound
	}
	return f, nil
}

// ListChildren returns the folders and files directly under folder (nil =
// the caller's root). A named folder is access-checked; the root is the
// caller's own. Scoped to the caller as owner (per-owner trees, M4b — when
// sharing lands this lists by parent and leans on folder access).
func (s *Service) ListChildren(ctx context.Context, caller pii.Subject, folder *string) (Listing, error) {
	if folder != nil {
		if err := s.authorizeFolder(ctx, caller, *folder); err != nil {
			return Listing{}, err
		}
	}
	rd := s.tx.Reader()
	folders, err := rd.Objects.FoldersInParent(ctx, caller, folder)
	if err != nil {
		return Listing{}, err
	}
	files, err := rd.Objects.ObjectsInFolder(ctx, caller, folder)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Folders: folders, Files: files}, nil
}

// Ancestors returns the folder and its ancestors root-first (for a
// breadcrumb), after an access check on the folder. The visited-set guards
// against a malformed cycle.
func (s *Service) Ancestors(ctx context.Context, caller pii.Subject, folderID string) ([]Folder, error) {
	if err := s.authorizeFolder(ctx, caller, folderID); err != nil {
		return nil, err
	}
	rd := s.tx.Reader()
	var chain []Folder
	seen := map[string]bool{}
	cur := &folderID
	for cur != nil && !seen[*cur] {
		seen[*cur] = true
		f, ok, err := rd.Objects.GetFolder(ctx, *cur)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		chain = append(chain, f)
		cur = f.ParentID
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// AllFolders returns every folder the caller owns (for a move-destination
// picker). Per-owner scope, like the listings (M4b).
func (s *Service) AllFolders(ctx context.Context, caller pii.Subject) ([]Folder, error) {
	return s.tx.Reader().Objects.FoldersByOwner(ctx, caller)
}

// RenameFile changes a file's display name.
func (s *Service) RenameFile(ctx context.Context, caller pii.Subject, objectID, name string) error {
	if name == "" {
		return fmt.Errorf("file: name required")
	}
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return err
	}
	o, ok, err := s.tx.Reader().Objects.GetObject(ctx, objectID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Objects.SetObjectName(ctx, objectID, name); err != nil {
			return err
		}
		if err := st.Audit.Emit(ctx, caller, "kamara.file.renamed",
			audit.Object{Type: Type, ID: objectID, Permalink: o.Permalink()}, nil); err != nil {
			return err
		}
		return st.Search.Feed(ctx, search.Doc{
			ID: objectID, Type: Type, Permalink: o.Permalink(), Owner: o.Owner, Text: name})
	})
}

// MoveFile relocates a file to dest (nil = root), updating its containment
// tuple. Content is untouched; the URL is stable (ADR-0007).
func (s *Service) MoveFile(ctx context.Context, caller pii.Subject, objectID string, dest *string) error {
	if err := s.authorize(ctx, caller, objectID); err != nil {
		return err
	}
	if dest != nil {
		if err := s.authorizeFolder(ctx, caller, *dest); err != nil {
			return err
		}
	}
	o, ok, err := s.tx.Reader().Objects.GetObject(ctx, objectID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Objects.SetObjectFolder(ctx, objectID, dest); err != nil {
			return err
		}
		return st.Audit.Emit(ctx, caller, "kamara.file.moved",
			audit.Object{Type: Type, ID: objectID, Permalink: o.Permalink()}, nil)
	}); err != nil {
		return err
	}
	return s.reparent(ctx, obj(objectID), o.FolderID, dest)
}

// RenameFolder changes a folder's name.
func (s *Service) RenameFolder(ctx context.Context, caller pii.Subject, folderID, name string) error {
	if name == "" {
		return fmt.Errorf("file: folder name required")
	}
	if err := s.authorizeFolder(ctx, caller, folderID); err != nil {
		return err
	}
	f, ok, err := s.tx.Reader().Objects.GetFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Objects.SetFolderName(ctx, folderID, name); err != nil {
			return err
		}
		if err := st.Audit.Emit(ctx, caller, "kamara.folder.renamed",
			audit.Object{Type: FolderType, ID: folderID, Permalink: f.Permalink()}, nil); err != nil {
			return err
		}
		return st.Search.Feed(ctx, search.Doc{
			ID: folderID, Type: FolderType, Permalink: f.Permalink(), Owner: f.Owner, Text: name})
	})
}

// MoveFolder relocates a folder under dest (nil = root), rejecting a move
// into its own subtree (cycle).
func (s *Service) MoveFolder(ctx context.Context, caller pii.Subject, folderID string, dest *string) error {
	if err := s.authorizeFolder(ctx, caller, folderID); err != nil {
		return err
	}
	if dest != nil {
		if err := s.authorizeFolder(ctx, caller, *dest); err != nil {
			return err
		}
		cyclic, err := s.wouldCycle(ctx, folderID, *dest)
		if err != nil {
			return err
		}
		if cyclic {
			return ErrCycle
		}
	}
	f, ok, err := s.tx.Reader().Objects.GetFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Objects.SetFolderParent(ctx, folderID, dest); err != nil {
			return err
		}
		return st.Audit.Emit(ctx, caller, "kamara.folder.moved",
			audit.Object{Type: FolderType, ID: folderID, Permalink: f.Permalink()}, nil)
	}); err != nil {
		return err
	}
	return s.reparent(ctx, folderObj(folderID), f.ParentID, dest)
}

// DeleteFolder removes an empty folder (empty-first, Kamara ADR-0002).
func (s *Service) DeleteFolder(ctx context.Context, caller pii.Subject, folderID string) error {
	if err := s.authorizeFolder(ctx, caller, folderID); err != nil {
		return err
	}
	rd := s.tx.Reader()
	has, err := rd.Objects.FolderHasChildren(ctx, folderID)
	if err != nil {
		return err
	}
	if has {
		return ErrNotEmpty
	}
	f, ok, err := rd.Objects.GetFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Audit.Emit(ctx, caller, "kamara.folder.deleted",
			audit.Object{Type: FolderType, ID: folderID, Permalink: f.Permalink()}, nil); err != nil {
			return err
		}
		if err := st.Objects.DeleteFolder(ctx, folderID); err != nil {
			return err
		}
		return st.Search.Remove(ctx, folderID)
	}); err != nil {
		return err
	}
	if f.ParentID != nil {
		_ = s.authz.DeleteObjectTuple(ctx, folderObj(*f.ParentID), ParentRelation, folderObj(folderID))
	}
	return s.authz.Delete(ctx, caller, Relation, folderObj(folderID))
}

// DeleteFolderTree removes a folder and everything under it — subfolders,
// files, versions, chunk references — the browser's "delete folder" (single-
// item DeleteFolder stays empty-first for the API's existing contract; the
// API opts in via ?recursive=true). The subtree walk AND the deletes run in
// one transaction: audit events, metadata deletes, and search removals
// commit atomically, children first so the parent_id/folder_id RESTRICT
// constraints hold. A child added concurrently trips that constraint and
// surfaces as ErrNotEmpty (retryable), never a partial delete; a concurrent
// move-out shrinks to the statement-level window the single-item deletes
// already accept (DeleteFolder's check-then-delete, #36). Blob reclaim,
// editing-session revocation, and OpenFGA tuple cleanup follow after commit,
// best-effort, exactly like the single-item deletes.
func (s *Service) DeleteFolderTree(ctx context.Context, caller pii.Subject, folderID string) error {
	if err := s.authorizeFolder(ctx, caller, folderID); err != nil {
		return err
	}
	root, ok, err := s.tx.Reader().Objects.GetFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	var folders []Folder
	var objects []Object
	var orphans []string
	if err := s.tx.InTx(ctx, func(st Stores) error {
		// Breadth-first collection (per-owner scope, like the listings);
		// the seen-set terminates a malformed cycle. Reset on entry so a
		// re-run transaction never sees a stale walk.
		folders, objects, orphans = []Folder{root}, nil, nil
		seen := map[string]bool{folderID: true}
		for i := 0; i < len(folders); i++ {
			fid := folders[i].ID
			objs, err := st.Objects.ObjectsInFolder(ctx, caller, &fid)
			if err != nil {
				return err
			}
			objects = append(objects, objs...)
			subs, err := st.Objects.FoldersInParent(ctx, caller, &fid)
			if err != nil {
				return err
			}
			for _, sub := range subs {
				if seen[sub.ID] {
					continue
				}
				seen[sub.ID] = true
				folders = append(folders, sub)
			}
		}
		for _, o := range objects {
			if err := st.Audit.Emit(ctx, caller, "kamara.file.deleted",
				audit.Object{Type: Type, ID: o.ID, Permalink: o.Permalink()}, nil); err != nil {
				return err
			}
			gone, err := reclaimObject(ctx, st, o.ID)
			if err != nil {
				return err
			}
			orphans = append(orphans, gone...)
			if err := st.Search.Remove(ctx, o.ID); err != nil {
				return err
			}
		}
		// Folders children-first (reverse of the breadth-first collection).
		for i := len(folders) - 1; i >= 0; i-- {
			f := folders[i]
			if err := st.Audit.Emit(ctx, caller, "kamara.folder.deleted",
				audit.Object{Type: FolderType, ID: f.ID, Permalink: f.Permalink()}, nil); err != nil {
				return err
			}
			if err := st.Objects.DeleteFolder(ctx, f.ID); err != nil {
				return err
			}
			if err := st.Search.Remove(ctx, f.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, h := range orphans {
		_ = s.blobs.Delete(ctx, h)
	}
	var tupleErr error
	keep := func(err error) {
		if err != nil && tupleErr == nil {
			tupleErr = err
		}
	}
	for _, o := range objects {
		if s.revoker != nil {
			_ = s.revoker.Revoke(ctx, o.ID)
		}
		if o.FolderID != nil {
			_ = s.authz.DeleteObjectTuple(ctx, folderObj(*o.FolderID), ParentRelation, obj(o.ID))
		}
		keep(s.authz.Delete(ctx, caller, Relation, obj(o.ID)))
	}
	for _, f := range folders {
		if f.ParentID != nil {
			_ = s.authz.DeleteObjectTuple(ctx, folderObj(*f.ParentID), ParentRelation, folderObj(f.ID))
		}
		keep(s.authz.Delete(ctx, caller, Relation, folderObj(f.ID)))
	}
	return tupleErr
}

// reparent updates the containment tuple of child (a fully-qualified object)
// from old to new folder (either nil), after the DB commit — the same
// out-of-transaction seam as the owner tuple (root ADR-0015). NOT
// self-healing: if the old-tuple delete fails, the stale tuple keeps the
// subtree reachable via its former parent (fail-open). Latent while trees
// are per-owner; must be closed before sharing (Kamara ADR-0002, issue).
func (s *Service) reparent(ctx context.Context, child string, old, dest *string) error {
	if old != nil {
		if err := s.authz.DeleteObjectTuple(ctx, folderObj(*old), ParentRelation, child); err != nil {
			return fmt.Errorf("file: clearing old parent tuple: %w", err)
		}
	}
	if dest != nil {
		if err := s.authz.WriteObjectTuple(ctx, folderObj(*dest), ParentRelation, child); err != nil {
			return fmt.Errorf("file: writing parent tuple: %w", err)
		}
	}
	return nil
}

// wouldCycle reports whether moving `moving` under `dest` would create a
// cycle — i.e. dest is `moving` itself or somewhere in its subtree. Walk
// from dest up to the root; hitting `moving` means dest is inside it. A
// pre-existing cycle in the ancestry (should be impossible, but a concurrent
// move could form one — issue tracked) is treated as a cycle so the walk
// terminates instead of looping forever.
func (s *Service) wouldCycle(ctx context.Context, moving, dest string) (bool, error) {
	rd := s.tx.Reader()
	seen := map[string]bool{}
	cur := &dest
	for cur != nil {
		if *cur == moving || seen[*cur] {
			return true, nil
		}
		seen[*cur] = true
		f, ok, err := rd.Objects.GetFolder(ctx, *cur)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		cur = f.ParentID
	}
	return false, nil
}
