package file

import (
	"context"

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// subjectData implements pii.SubjectData for files (ADR-0009 §3): export
// and erase a person's files. If export can find it, erase can remove it.
type subjectData struct {
	repo   Repo
	blobs  blob.Store
	search *search.Feeder
}

// ExportSubject returns the subject's file metadata (names, sizes, ids).
func (d *subjectData) ExportSubject(ctx context.Context, s pii.Subject) (any, error) {
	objs, err := d.repo.ByOwner(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, nil
	}
	out := make([]map[string]any, 0, len(objs))
	for _, o := range objs {
		out = append(out, map[string]any{
			"id": o.ID, "name": o.Name, "size": o.Size,
			"created": o.Created, "permalink": o.Permalink(),
		})
	}
	return out, nil
}

// EraseSubject deletes the subject's files: object rows (cascading versions
// and manifest), decrement chunk ref-counts, and reclaim orphaned chunk
// rows + blobs. Source-before-derived ordering (ADR-0009 §3); best-effort
// per object (the whole-tenant transactional erasure orchestrator is later).
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
		// Remove the derived search entry after the source (ADR-0009 §3).
		if err := d.search.Remove(ctx, o.ID); err != nil {
			return err
		}
	}
	return nil
}
