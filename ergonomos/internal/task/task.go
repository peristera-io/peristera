// Package task is Ergonomos's task domain: the single-user task stub
// (M3b). Its point is to prove the four cross-cutting conventions compose
// — every mutation flows through personal-data metadata (lib/pii),
// authorization (lib/authz), audit (lib/audit), and the search feed
// (lib/search). Multi-user, the block editor, and calendar entries are out
// (2027).
package task

import (
	"context"
	"fmt"
	"time"

	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/id"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

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

// Service wires the task domain through the four conventions.
type Service struct {
	repo   Repo
	authz  Authorizer
	audit  *audit.Emitter
	search *search.Feeder
	now    func() time.Time
}

// NewService builds the service and registers the task personal-data
// descriptor (ADR-0009) into reg — a task relates to its owner, so it is
// exportable and erasable per subject. Pass pii.Default in production; a
// fresh pii.NewRegistry() in tests.
func NewService(reg *pii.Registry, repo Repo, az Authorizer, em *audit.Emitter, sf *search.Feeder) *Service {
	s := &Service{repo: repo, authz: az, audit: em, search: sf, now: time.Now}
	reg.Register(pii.Descriptor{
		Type:   Type,
		Fields: []string{"title"},
		Hooks:  &subjectData{repo: repo, search: sf},
	})
	return s
}

func obj(id string) string { return Type + ":" + id }

// Create adds a task owned by owner and runs the full convention chain.
func (s *Service) Create(ctx context.Context, owner pii.Subject, title string) (Task, error) {
	if title == "" {
		return Task{}, fmt.Errorf("task: title required")
	}
	now := s.now().UTC()
	t := Task{ID: id.V7(), Owner: owner, Title: title, Created: now, Updated: now}
	if err := s.repo.Insert(ctx, t); err != nil {
		return Task{}, err
	}
	// Authorization tuple after the row commits (ADR-0010 Consequences).
	if err := s.authz.Write(ctx, owner, Relation, obj(t.ID)); err != nil {
		return Task{}, fmt.Errorf("task: writing owner tuple: %w", err)
	}
	if err := s.audit.Emit(ctx, owner, "ergonomos.task.created",
		audit.Object{Type: Type, ID: t.ID, Permalink: t.Permalink()}, nil); err != nil {
		return Task{}, err
	}
	if err := s.feed(ctx, t); err != nil {
		return Task{}, err
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
	return s.repo.ByIDs(ctx, ids)
}

// SetDone toggles completion, after an authorization check.
func (s *Service) SetDone(ctx context.Context, caller pii.Subject, taskID string, done bool) (Task, error) {
	if err := s.authorize(ctx, caller, taskID); err != nil {
		return Task{}, err
	}
	t, err := s.repo.SetDone(ctx, taskID, done)
	if err != nil {
		return Task{}, err
	}
	action := audit.Action("ergonomos.task.reopened")
	if done {
		action = "ergonomos.task.completed"
	}
	if err := s.audit.Emit(ctx, caller, action,
		audit.Object{Type: Type, ID: t.ID, Permalink: t.Permalink()}, nil); err != nil {
		return Task{}, err
	}
	return t, s.feed(ctx, t)
}

// Delete removes a task after an authorization check, unwinding every
// convention (tuple, search entry; the audit event is kept, append-only).
func (s *Service) Delete(ctx context.Context, caller pii.Subject, taskID string) error {
	if err := s.authorize(ctx, caller, taskID); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, taskID); err != nil {
		return err
	}
	if err := s.authz.Delete(ctx, caller, Relation, obj(taskID)); err != nil {
		return err
	}
	if err := s.search.Remove(ctx, taskID); err != nil {
		return err
	}
	return s.audit.Emit(ctx, caller, "ergonomos.task.deleted",
		audit.Object{Type: Type, ID: taskID, Permalink: "/tasks/" + taskID}, nil)
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

func (s *Service) feed(ctx context.Context, t Task) error {
	return s.search.Feed(ctx, search.Doc{
		ID: t.ID, Type: Type, Permalink: t.Permalink(),
		Owner: t.Owner, Text: t.Title,
	})
}
