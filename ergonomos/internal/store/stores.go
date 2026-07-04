package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/lib/dbtx"
	"github.com/peristera-io/peristera/lib/pii"
)

// --- task.Repo ---

type TaskRepo struct{ db dbtx.Executor }

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
