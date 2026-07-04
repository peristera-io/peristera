package task

import (
	"context"

	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// subjectData implements pii.SubjectData for tasks (ADR-0009 §3): export
// and erase a person's tasks. If export can find it, erase can remove it.
type subjectData struct {
	repo   Repo
	search *search.Feeder
}

// ExportSubject returns the subject's tasks in a machine-readable form.
func (d *subjectData) ExportSubject(ctx context.Context, s pii.Subject) (any, error) {
	tasks, err := d.repo.ByOwner(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{
			"id": t.ID, "title": t.Title, "done": t.Done,
			"created": t.Created, "permalink": t.Permalink(),
		})
	}
	return out, nil
}

// EraseSubject deletes the subject's tasks (source) and their search
// entries. The erasure-ordering rule (ADR-0009 §3) is honored: source rows
// go first, then the derived search entries.
func (d *subjectData) EraseSubject(ctx context.Context, s pii.Subject) error {
	tasks, err := d.repo.ByOwner(ctx, s)
	if err != nil {
		return err
	}
	if err := d.repo.DeleteByOwner(ctx, s); err != nil {
		return err
	}
	for _, t := range tasks {
		if err := d.search.Remove(ctx, t.ID); err != nil {
			return err
		}
	}
	return nil
}
