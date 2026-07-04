package pii

import (
	"context"
	"errors"
	"testing"
)

type fakeHooks struct {
	data      map[string]any // subject.String() → export payload
	erased    map[string]bool
	exportErr error
}

func (f *fakeHooks) ExportSubject(_ context.Context, s Subject) (any, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return f.data[s.String()], nil
}
func (f *fakeHooks) EraseSubject(_ context.Context, s Subject) error {
	if f.erased == nil {
		f.erased = map[string]bool{}
	}
	f.erased[s.String()] = true
	return nil
}

func TestRegisterAndRegistryIsSorted(t *testing.T) {
	resetRegistry()
	Register(Descriptor{Type: "zeta/z", Hooks: &fakeHooks{}})
	Register(Descriptor{Type: "alpha/a", Hooks: &fakeHooks{}, Retention: RetentionClass{Name: "x"}})

	reg := Registry()
	if len(reg) != 2 || reg[0].Type != "alpha/a" || reg[1].Type != "zeta/z" {
		t.Fatalf("Registry not sorted: %+v", reg)
	}
	// Default retention applied when unset.
	if reg[1].Retention.Name != "none" {
		t.Errorf("unset retention should default to none, got %q", reg[1].Retention.Name)
	}
}

func TestRegisterGuards(t *testing.T) {
	resetRegistry()
	mustPanic(t, "empty type", func() { Register(Descriptor{Hooks: &fakeHooks{}}) })
	mustPanic(t, "nil hooks", func() { Register(Descriptor{Type: "a/b"}) })
	Register(Descriptor{Type: "a/b", Hooks: &fakeHooks{}})
	mustPanic(t, "duplicate type", func() { Register(Descriptor{Type: "a/b", Hooks: &fakeHooks{}}) })
}

func TestExportAndEraseAcrossTypes(t *testing.T) {
	resetRegistry()
	ctx := context.Background()
	alice := Subject{Instance: "demo.example", UserID: "alice"}
	tasks := &fakeHooks{data: map[string]any{alice.String(): []string{"buy milk"}}}
	notes := &fakeHooks{data: map[string]any{}}
	Register(Descriptor{Type: "ergonomos/task", Hooks: tasks})
	Register(Descriptor{Type: "ergonomos/note", Hooks: notes})

	out, err := ExportSubject(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["ergonomos/task"]; !ok {
		t.Error("task data missing from export")
	}
	if _, ok := out["ergonomos/note"]; ok {
		t.Error("empty note data should be omitted from export")
	}

	if err := EraseSubject(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if !tasks.erased[alice.String()] || !notes.erased[alice.String()] {
		t.Error("erase did not reach every registered type")
	}
}

func TestExportPropagatesHookError(t *testing.T) {
	resetRegistry()
	Register(Descriptor{Type: "a/b", Hooks: &fakeHooks{exportErr: errors.New("boom")}})
	if _, err := ExportSubject(context.Background(), Subject{Instance: "i", UserID: "u"}); err == nil {
		t.Error("export must propagate a hook error")
	}
}

// resetRegistry clears package state between tests (test-only).
func resetRegistry() {
	regMu.Lock()
	defer regMu.Unlock()
	descriptors = map[string]Descriptor{}
}

// resetClasses restores the retention taxonomy to just None (test-only),
// mirroring resetRegistry so a test's RegisterClass doesn't leak.
func resetClasses() {
	regMu.Lock()
	defer regMu.Unlock()
	classes = map[string]RetentionClass{None.Name: None}
}

func mustPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", what)
		}
	}()
	fn()
}
