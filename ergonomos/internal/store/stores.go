package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// --- task.Repo ---

type TaskRepo struct{ db *sql.DB }

func (r *TaskRepo) Insert(ctx context.Context, t task.Task) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tasks (id, owner_instance, owner_user, title, done, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		t.ID, t.Owner.Instance, t.Owner.UserID, t.Title, t.Done, t.Created, t.Updated)
	return err
}

func scanTask(row interface{ Scan(...any) error }) (task.Task, error) {
	var t task.Task
	err := row.Scan(&t.ID, &t.Owner.Instance, &t.Owner.UserID, &t.Title, &t.Done, &t.Created, &t.Updated)
	return t, err
}

const taskCols = `id, owner_instance, owner_user, title, done, created_at, updated_at`

func (r *TaskRepo) Get(ctx context.Context, id string) (task.Task, bool, error) {
	t, err := scanTask(r.db.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, false, nil
	}
	return t, err == nil, err
}

func (r *TaskRepo) ByIDs(ctx context.Context, ids []string) ([]task.Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id = ANY($1) ORDER BY created_at`, ids)
	if err != nil {
		return nil, err
	}
	return collectTasks(rows)
}

func (r *TaskRepo) ByOwner(ctx context.Context, o pii.Subject) ([]task.Task, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+taskCols+` FROM tasks WHERE owner_instance=$1 AND owner_user=$2 ORDER BY created_at`,
		o.Instance, o.UserID)
	if err != nil {
		return nil, err
	}
	return collectTasks(rows)
}

func collectTasks(rows *sql.Rows) ([]task.Task, error) {
	defer rows.Close()
	var out []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TaskRepo) SetDone(ctx context.Context, id string, done bool) (task.Task, error) {
	t, err := scanTask(r.db.QueryRowContext(ctx,
		`UPDATE tasks SET done=$2, updated_at=now() WHERE id=$1 RETURNING `+taskCols, id, done))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, task.ErrNotFound
	}
	return t, err
}

func (r *TaskRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id=$1`, id)
	return err
}

func (r *TaskRepo) DeleteByOwner(ctx context.Context, o pii.Subject) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE owner_instance=$1 AND owner_user=$2`, o.Instance, o.UserID)
	return err
}

var _ task.Repo = (*TaskRepo)(nil)

// --- pii.PseudonymStore ---

type PseudonymStore struct{ db *sql.DB }

func (p *PseudonymStore) Lookup(ctx context.Context, s pii.Subject) (string, bool, error) {
	var tok string
	err := p.db.QueryRowContext(ctx,
		`SELECT token FROM subject_pseudonyms WHERE instance=$1 AND user_id=$2`, s.Instance, s.UserID).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return tok, err == nil, err
}

func (p *PseudonymStore) Save(ctx context.Context, token string, s pii.Subject) error {
	// UNIQUE(instance,user_id) enforces one token per subject; a duplicate
	// subject errors, which lib/pii's TokenFor handles by re-reading.
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO subject_pseudonyms (token, instance, user_id) VALUES ($1,$2,$3)`,
		token, s.Instance, s.UserID)
	return err
}

func (p *PseudonymStore) Resolve(ctx context.Context, token string) (pii.Subject, bool, error) {
	var s pii.Subject
	err := p.db.QueryRowContext(ctx,
		`SELECT instance, user_id FROM subject_pseudonyms WHERE token=$1`, token).Scan(&s.Instance, &s.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return pii.Subject{}, false, nil
	}
	return s, err == nil, err
}

func (p *PseudonymStore) Delete(ctx context.Context, s pii.Subject) error {
	_, err := p.db.ExecContext(ctx,
		`DELETE FROM subject_pseudonyms WHERE instance=$1 AND user_id=$2`, s.Instance, s.UserID)
	return err
}

var _ pii.PseudonymStore = (*PseudonymStore)(nil)

// --- audit.Sink ---

type AuditSink struct{ db *sql.DB }

func (a *AuditSink) Append(ctx context.Context, e audit.Event) error {
	var detail []byte
	if e.Detail != nil {
		var err error
		if detail, err = json.Marshal(e.Detail); err != nil {
			return err
		}
	}
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO audit_events (id, at, actor_token, action, object_type, object_id, object_permalink, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.ID, e.Time, e.ActorToken, string(e.Action),
		e.Object.Type, e.Object.ID, e.Object.Permalink, detail)
	return err
}

var _ audit.Sink = (*AuditSink)(nil)

// --- search.Index ---

type SearchIndex struct{ db *sql.DB }

func (s *SearchIndex) Upsert(ctx context.Context, d search.Doc) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO search_documents (id, doc_type, permalink, owner_instance, owner_user, body)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO UPDATE SET doc_type=EXCLUDED.doc_type, permalink=EXCLUDED.permalink,
		   owner_instance=EXCLUDED.owner_instance, owner_user=EXCLUDED.owner_user, body=EXCLUDED.body`,
		d.ID, d.Type, d.Permalink, d.Owner.Instance, d.Owner.UserID, d.Text)
	return err
}

func (s *SearchIndex) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM search_documents WHERE id=$1`, id)
	return err
}

var _ search.Index = (*SearchIndex)(nil)
