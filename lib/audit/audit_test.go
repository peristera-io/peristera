package audit

import (
	"context"
	"testing"
	"time"

	"github.com/peristera-io/peristera/lib/pii"
)

// capturingSink records appended events (append-only, like the real store).
type capturingSink struct{ events []Event }

func (c *capturingSink) Append(_ context.Context, e Event) error {
	c.events = append(c.events, e)
	return nil
}

func TestEmitStoresPseudonymNotRawSubject(t *testing.T) {
	ctx := context.Background()
	sink := &capturingSink{}
	pseud := pii.NewInMemoryPseudonyms()
	em := NewEmitter(sink, pseud)
	// Deterministic time for the assertion.
	em.now = func() time.Time { return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC) }

	actor := pii.Subject{Instance: "demo.example", UserID: "alice"}
	obj := Object{Type: "ergonomos/task", ID: "0198c", Permalink: "/tasks/0198c"}
	if err := em.Emit(ctx, actor, "ergonomos.task.completed", obj, map[string]any{"title": "milk"}); err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(sink.events))
	}
	e := sink.events[0]
	// The raw subject must NOT appear; the actor is a pseudonym token.
	if e.ActorToken == "" || e.ActorToken == actor.String() || e.ActorToken == actor.UserID {
		t.Errorf("actor stored as raw subject or empty: %q", e.ActorToken)
	}
	// The token must resolve back to the subject (audit-viewer path).
	got, ok, _ := pseud.Resolve(ctx, e.ActorToken)
	if !ok || got != actor {
		t.Errorf("token does not resolve to actor: %v,%v", got, ok)
	}
	if e.ID == "" || e.Action != "ergonomos.task.completed" || e.Object != obj {
		t.Errorf("event fields wrong: %+v", e)
	}
	if !e.Time.Equal(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("time = %v", e.Time)
	}
}

func TestEmitStableActorTokenAcrossEvents(t *testing.T) {
	ctx := context.Background()
	sink := &capturingSink{}
	em := NewEmitter(sink, pii.NewInMemoryPseudonyms())
	actor := pii.Subject{Instance: "demo.example", UserID: "bob"}

	for range 3 {
		if err := em.Emit(ctx, actor, "ergonomos.task.created", Object{Type: "ergonomos/task", ID: "x"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tok := sink.events[0].ActorToken
	for _, e := range sink.events {
		if e.ActorToken != tok {
			t.Errorf("actor token not stable across events: %q vs %q", e.ActorToken, tok)
		}
	}
}
