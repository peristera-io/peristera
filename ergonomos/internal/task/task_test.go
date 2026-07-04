package task

import (
	"context"
	"slices"
	"testing"

	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// --- in-memory fakes ---

type memRepo struct{ tasks map[string]Task }

func newMemRepo() *memRepo                                { return &memRepo{tasks: map[string]Task{}} }
func (m *memRepo) Insert(_ context.Context, t Task) error { m.tasks[t.ID] = t; return nil }
func (m *memRepo) Get(_ context.Context, id string) (Task, bool, error) {
	t, ok := m.tasks[id]
	return t, ok, nil
}
func (m *memRepo) ByIDs(_ context.Context, ids []string) ([]Task, error) {
	var out []Task
	for _, id := range ids {
		if t, ok := m.tasks[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memRepo) ByOwner(_ context.Context, o pii.Subject) ([]Task, error) {
	var out []Task
	for _, t := range m.tasks {
		if t.Owner == o {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memRepo) SetDone(_ context.Context, id string, done bool) (Task, error) {
	t := m.tasks[id]
	t.Done = done
	m.tasks[id] = t
	return t, nil
}
func (m *memRepo) Delete(_ context.Context, id string) error { delete(m.tasks, id); return nil }
func (m *memRepo) DeleteByOwner(_ context.Context, o pii.Subject) error {
	for id, t := range m.tasks {
		if t.Owner == o {
			delete(m.tasks, id)
		}
	}
	return nil
}

// memAuthz records tuples: object → set of "relation|user".
type memAuthz struct{ tuples map[string]bool }

func newMemAuthz() *memAuthz                    { return &memAuthz{tuples: map[string]bool{}} }
func key(u pii.Subject, rel, obj string) string { return obj + "|" + rel + "|" + u.String() }
func (a *memAuthz) Write(_ context.Context, u pii.Subject, rel, obj string) error {
	a.tuples[key(u, rel, obj)] = true
	return nil
}
func (a *memAuthz) Delete(_ context.Context, u pii.Subject, rel, obj string) error {
	delete(a.tuples, key(u, rel, obj))
	return nil
}
func (a *memAuthz) Check(_ context.Context, u pii.Subject, rel, obj string) (bool, error) {
	return a.tuples[key(u, rel, obj)], nil
}
func (a *memAuthz) ListObjects(_ context.Context, u pii.Subject, rel, typ string) ([]string, error) {
	var ids []string
	for k := range a.tuples {
		// k = "type:id|relation|user"
		var o, r, us string
		parts := splitN(k, '|')
		if len(parts) != 3 {
			continue
		}
		o, r, us = parts[0], parts[1], parts[2]
		if r == rel && us == u.String() && len(o) > len(typ) && o[:len(typ)+1] == typ+":" {
			ids = append(ids, o[len(typ)+1:])
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func splitN(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

type memSink struct{ events []audit.Event }

func (m *memSink) Append(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

type memIndex struct{ docs map[string]search.Doc }

func (m *memIndex) Upsert(_ context.Context, d search.Doc) error { m.docs[d.ID] = d; return nil }
func (m *memIndex) Delete(_ context.Context, id string) error    { delete(m.docs, id); return nil }

func newService(t *testing.T) (*Service, *pii.Registry, *memRepo, *memAuthz, *memSink, *memIndex) {
	t.Helper()
	repo := newMemRepo()
	az := newMemAuthz()
	sink := &memSink{}
	idx := &memIndex{docs: map[string]search.Doc{}}
	em := audit.NewEmitter(sink, pii.NewInMemoryPseudonyms())
	sf := search.NewFeeder(idx)
	reg := pii.NewRegistry() // fresh per test — no global registration clash
	return NewService(reg, repo, az, em, sf), reg, repo, az, sink, idx
}

func TestCreateRunsEveryConvention(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, az, sink, idx := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	tk, err := svc.Create(ctx, alice, "buy milk")
	if err != nil {
		t.Fatal(err)
	}
	// Repo has it.
	if _, ok, _ := repo.Get(ctx, tk.ID); !ok {
		t.Error("task not persisted")
	}
	// Authorization tuple written.
	if ok, _ := az.Check(ctx, alice, Relation, obj(tk.ID)); !ok {
		t.Error("owner tuple not written")
	}
	// Audit event emitted, actor pseudonymized (not raw subject).
	if len(sink.events) != 1 || sink.events[0].Action != "ergonomos.task.created" {
		t.Fatalf("audit events = %+v", sink.events)
	}
	if sink.events[0].ActorToken == alice.String() || sink.events[0].ActorToken == "" {
		t.Error("audit actor not pseudonymized")
	}
	// Search fed.
	if idx.docs[tk.ID].Text != "buy milk" {
		t.Error("task not fed to search")
	}
}

func TestListIsPermissionFiltered(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}

	a1, _ := svc.Create(ctx, alice, "alice one")
	_, _ = svc.Create(ctx, bob, "bob one")

	got, err := svc.List(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != a1.ID {
		t.Errorf("List returned other owners' tasks: %+v", got)
	}
}

func TestDeleteUnauthorized(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _ := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	bob := pii.Subject{Instance: "demo.example", UserID: "bob"}

	tk, _ := svc.Create(ctx, alice, "alice's")
	if err := svc.Delete(ctx, bob, tk.ID); err == nil {
		t.Error("bob must not be able to delete alice's task")
	}
}

func TestExportAndErase(t *testing.T) {
	ctx := context.Background()
	svc, reg, _, _, _, idx := newService(t)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	tk, _ := svc.Create(ctx, alice, "secret")

	out, err := reg.ExportSubject(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[Type]; !ok {
		t.Error("export did not include the task")
	}
	if err := reg.EraseSubject(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.docs[tk.ID]; ok {
		t.Error("search entry not removed on erase")
	}
	after, _ := reg.ExportSubject(ctx, alice)
	if _, ok := after[Type]; ok {
		t.Error("task still exportable after erase")
	}
}
