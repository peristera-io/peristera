package file

import (
	"context"
	"sort"

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// subjectData implements pii.SubjectData for the file domain (ADR-0009 §3):
// export and erase a person's files and folders. If export can find it,
// erase can remove it.
type subjectData struct {
	repo   Repo
	blobs  blob.Store
	search *search.Feeder
	authz  Authorizer
}

// ExportSubject returns the subject's file and folder metadata.
func (d *subjectData) ExportSubject(ctx context.Context, s pii.Subject) (any, error) {
	objs, err := d.repo.ByOwner(ctx, s)
	if err != nil {
		return nil, err
	}
	folders, err := d.repo.FoldersByOwner(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 && len(folders) == 0 {
		return nil, nil
	}
	files := make([]map[string]any, 0, len(objs))
	for _, o := range objs {
		files = append(files, map[string]any{
			"id": o.ID, "name": o.Name, "size": o.Size, "folder": o.FolderID,
			"created": o.Created, "permalink": o.Permalink(),
		})
	}
	fdrs := make([]map[string]any, 0, len(folders))
	for _, f := range folders {
		fdrs = append(fdrs, map[string]any{
			"id": f.ID, "name": f.Name, "parent": f.ParentID,
			"created": f.Created, "permalink": f.Permalink(),
		})
	}
	return map[string]any{"files": files, "folders": fdrs}, nil
}

// EraseSubject deletes the subject's files (reclaiming chunks) then folders,
// with their search entries and OpenFGA tuples. Source-before-derived
// ordering (ADR-0009 §3); best-effort per item (the whole-tenant
// transactional erasure orchestrator is later). Folders go deepest-first so
// a parent is never removed before its children (parent_id is RESTRICT).
func (d *subjectData) EraseSubject(ctx context.Context, s pii.Subject) error {
	objs, err := d.repo.ByOwner(ctx, s)
	if err != nil {
		return err
	}
	for _, o := range objs {
		hashes, err := d.repo.ChunkHashesOf(ctx, o.ID)
		if err != nil {
			return err
		}
		if err := d.repo.DeleteObject(ctx, o.ID); err != nil {
			return err
		}
		orphans, err := d.repo.DecRef(ctx, hashes)
		if err != nil {
			return err
		}
		if err := d.repo.DeleteChunks(ctx, orphans); err != nil {
			return err
		}
		for _, h := range orphans {
			_ = d.blobs.Delete(ctx, h)
		}
		if err := d.search.Remove(ctx, o.ID); err != nil {
			return err
		}
		if o.FolderID != nil {
			_ = d.authz.DeleteObjectTuple(ctx, folderObj(*o.FolderID), ParentRelation, obj(o.ID))
		}
		if err := d.authz.Delete(ctx, s, Relation, obj(o.ID)); err != nil {
			return err
		}
	}

	folders, err := d.repo.FoldersByOwner(ctx, s)
	if err != nil {
		return err
	}
	for _, f := range deepestFirst(folders) {
		if err := d.repo.DeleteFolder(ctx, f.ID); err != nil {
			return err
		}
		if err := d.search.Remove(ctx, f.ID); err != nil {
			return err
		}
		if f.ParentID != nil {
			_ = d.authz.DeleteObjectTuple(ctx, folderObj(*f.ParentID), ParentRelation, folderObj(f.ID))
		}
		if err := d.authz.Delete(ctx, s, Relation, folderObj(f.ID)); err != nil {
			return err
		}
	}
	return nil
}

// deepestFirst orders folders so descendants precede their ancestors, using
// the in-memory parent map (depth = steps to a root among this set).
func deepestFirst(fs []Folder) []Folder {
	byID := make(map[string]Folder, len(fs))
	for _, f := range fs {
		byID[f.ID] = f
	}
	depth := func(f Folder) int {
		d, cur := 0, f.ParentID
		seen := map[string]bool{f.ID: true} // guard against a malformed cycle
		for cur != nil {
			if seen[*cur] {
				break
			}
			seen[*cur] = true
			p, ok := byID[*cur]
			if !ok {
				break
			}
			d++
			cur = p.ParentID
		}
		return d
	}
	out := append([]Folder(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool { return depth(out[i]) > depth(out[j]) })
	return out
}
