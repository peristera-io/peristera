// Package task is Ergonomos's task domain: the single-user task stub
// (M3b). Its point is to prove the four cross-cutting conventions compose
// — every mutation flows through personal-data metadata (lib/pii),
// authorization (lib/authz), audit (lib/audit), and the search feed
// (lib/search). Multi-user, the block editor, and calendar entries are out
// (2027).
package task

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

// ErrNotFound is returned when a task id does not exist (distinct from
// "not authorized"), so handlers can answer 404 rather than 403/500.
var ErrNotFound = errors.New("task: not found")

// Type is the app-namespaced object type (ADR-0007/0010).
const Type = "ergonomos/task"

// Relation names the OpenFGA relation for task ownership.
const Relation = "owner"

// Task is one to-do. Owner is stored for display; authorization is
// OpenFGA's (ADR-0010 §4), never this column.
type Task struct {
	ID      string
	Owner   pii.Subject
	Title   string
	Done    bool
	Created time.Time
	Updated time.Time
}

// Permalink is the canonical URL (ADR-0007): identity is the ID, never a
// path or title.
func (t Task) Permalink() string { return "/tasks/" + t.ID }

// Repo is the task persistence port (Postgres in production, in-memory in
// tests).
type Repo interface {
	Insert(ctx context.Context, t Task) error
	Get(ctx context.Context, id string) (Task, bool, error)
	ByIDs(ctx context.Context, ids []string) ([]Task, error)
	ByOwner(ctx context.Context, owner pii.Subject) ([]Task, error)
	SetDone(ctx context.Context, id string, done bool) (Task, error)
	Delete(ctx context.Context, id string) error
	DeleteByOwner(ctx context.Context, owner pii.Subject) error
}

// Authorizer is the subset of lib/authz the domain uses (an interface so
// the domain is unit-testable without a live OpenFGA).
type Authorizer interface {
	Write(ctx context.Context, user pii.Subject, relation, object string) error
	Delete(ctx context.Context, user pii.Subject, relation, object string) error
	Check(ctx context.Context, user pii.Subject, relation, object string) (bool, error)
	ListObjects(ctx context.Context, user pii.Subject, relation, objectType string) ([]string, error)
}

// Stores bundles the same-database convention stores of one transaction
// (or of a read): the task rows, the audit emitter, the search feeder.
// A TxRunner builds one over a *sql.Tx inside InTx, and over the *sql.DB
// for Reader (ADR-0015).
type Stores struct {
	Tasks  Repo
	Audit  *audit.Emitter
	Search *search.Feeder
}

// TxRunner runs a mutation's same-DB writes atomically (InTx) and provides
// a non-transactional bundle for reads and export/erase (Reader).
type TxRunner interface {
	InTx(ctx context.Context, fn func(Stores) error) error
	Reader() Stores
}

// Service wires the task domain through the four conventions. Each mutation
// runs the same-database writes (row + audit + search) in ONE transaction
// via the TxRunner (ADR-0015); the OpenFGA tuple write is the one step that
// stays outside (a separate system) — the single documented remaining seam.
type Service struct {
	tx    TxRunner
	authz Authorizer
	now   func() time.Time
}

// NewService builds the service and registers the task personal-data
// descriptor (ADR-0009) into reg. Pass pii.Default in production; a fresh
// pii.NewRegistry() in tests.
func NewService(reg *pii.Registry, txr TxRunner, az Authorizer) *Service {
	s := &Service{tx: txr, authz: az, now: time.Now}
	rd := txr.Reader()
	reg.Register(pii.Descriptor{
		Type:   Type,
		Fields: []string{"title"},
		Hooks:  &subjectData{repo: rd.Tasks, search: rd.Search},
	})
	return s
}

func obj(id string) string { return Type + ":" + id }

func doc(t Task) search.Doc {
	return search.Doc{ID: t.ID, Type: Type, Permalink: t.Permalink(), Owner: t.Owner, Text: t.Title}
}

// Create adds a task owned by owner and runs the full convention chain:
// row + audit + search atomically in one transaction, then the OpenFGA
// tuple (outside the transaction — the remaining seam is a committed task
// with a not-yet-written tuple, which is simply invisible until the tuple
// lands or the create is retried).
func (s *Service) Create(ctx context.Context, owner pii.Subject, title string) (Task, error) {
	if title == "" {
		return Task{}, fmt.Errorf("task: title required")
	}
	now := s.now().UTC()
	t := Task{ID: id.V7(), Owner: owner, Title: title, Created: now, Updated: now}
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Tasks.Insert(ctx, t); err != nil {
			return err
		}
		if err := st.Audit.Emit(ctx, owner, "ergonomos.task.created",
			audit.Object{Type: Type, ID: t.ID, Permalink: t.Permalink()}, nil); err != nil {
			return err
		}
		return st.Search.Feed(ctx, doc(t))
	}); err != nil {
		return Task{}, err
	}
	if err := s.authz.Write(ctx, owner, Relation, obj(t.ID)); err != nil {
		return Task{}, fmt.Errorf("task: writing owner tuple: %w", err)
	}
	return t, nil
}

// List returns the caller's tasks, permission-filtered through OpenFGA
// (ADR-0010 §4) — never a WHERE owner = ... on the table.
func (s *Service) List(ctx context.Context, caller pii.Subject) ([]Task, error) {
	ids, err := s.authz.ListObjects(ctx, caller, Relation, Type)
	if err != nil {
		return nil, err
	}
	return s.tx.Reader().Tasks.ByIDs(ctx, ids)
}

// SetDone toggles completion (after an authorization check): the row
// update, audit event, and search re-feed are one transaction.
func (s *Service) SetDone(ctx context.Context, caller pii.Subject, taskID string, done bool) (Task, error) {
	if err := s.authorize(ctx, caller, taskID); err != nil {
		return Task{}, err
	}
	action := audit.Action("ergonomos.task.reopened")
	if done {
		action = "ergonomos.task.completed"
	}
	var out Task
	err := s.tx.InTx(ctx, func(st Stores) error {
		t, err := st.Tasks.SetDone(ctx, taskID, done)
		if err != nil {
			return err
		}
		out = t
		if err := st.Audit.Emit(ctx, caller, action,
			audit.Object{Type: Type, ID: t.ID, Permalink: t.Permalink()}, nil); err != nil {
			return err
		}
		return st.Search.Feed(ctx, doc(t))
	})
	return out, err
}

// Delete removes a task (after an authorization check): the row delete,
// audit event, and search removal are one transaction — so a task is never
// destroyed without its audit record (ADR-0011 §2). The OpenFGA tuple
// delete is outside; a residual dangling tuple is harmless (invisible task).
func (s *Service) Delete(ctx context.Context, caller pii.Subject, taskID string) error {
	if err := s.authorize(ctx, caller, taskID); err != nil {
		return err
	}
	if err := s.tx.InTx(ctx, func(st Stores) error {
		if err := st.Audit.Emit(ctx, caller, "ergonomos.task.deleted",
			audit.Object{Type: Type, ID: taskID, Permalink: "/tasks/" + taskID}, nil); err != nil {
			return err
		}
		if err := st.Tasks.Delete(ctx, taskID); err != nil {
			return err
		}
		return st.Search.Remove(ctx, taskID)
	}); err != nil {
		return err
	}
	return s.authz.Delete(ctx, caller, Relation, obj(taskID))
}

func (s *Service) authorize(ctx context.Context, caller pii.Subject, taskID string) error {
	ok, err := s.authz.Check(ctx, caller, Relation, obj(taskID))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("task: %s not authorized on %s", caller, taskID)
	}
	return nil
}
