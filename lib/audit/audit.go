// Package audit implements the audit-event convention (ADR-0011): every
// mutation emits a typed, per-tenant, append-only event. The actor is
// stored as a per-subject pseudonym token (ADR-0011 §4, via lib/pii), so
// an append-only row never carries a raw subject ID and a person stays
// erasable by dropping their pseudonym mapping.
package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/peristera-io/peristera/lib/id"
	"github.com/peristera-io/peristera/lib/pii"
)

// Action is a typed verb, enumerated per app (e.g. "ergonomos.task.completed").
// Not free-text, so the log stays queryable.
type Action string

// Object identifies what an event acted on (ADR-0011 §1): its namespaced
// type, UUIDv7 id, and canonical permalink (ADR-0007).
type Object struct {
	Type      string
	ID        string
	Permalink string
}

// Event is one audit record. Append-only: never updated or deleted.
type Event struct {
	ID         string // UUIDv7
	Time       time.Time
	ActorToken string // pii pseudonym token — NOT the raw subject
	Action     Action
	Object     Object
	// Detail is optional structured context. WARNING: it lands in an
	// append-only row, so it must NOT contain raw subject IDs or other
	// personal data — that would be un-erasable and defeat the pseudonym
	// guarantee (ADR-0011 §4). Reference people by pseudonym token or omit.
	Detail map[string]any
}

// Sink is the append-only store events are written to. Implementations
// must be append-only at the storage level (no UPDATE/DELETE grants).
type Sink interface {
	Append(ctx context.Context, e Event) error
}

// Emitter records events. It holds the tenant's pseudonym allocator so the
// actor is always pseudonymized before storage — the structural guarantee
// behind ADR-0011 §4 (apps cannot accidentally persist a raw subject ID).
type Emitter struct {
	sink  Sink
	pseud *pii.Pseudonyms
	now   func() time.Time // injectable for tests
}

// NewEmitter builds an emitter over a sink and the tenant's pseudonyms.
func NewEmitter(sink Sink, pseud *pii.Pseudonyms) *Emitter {
	return &Emitter{sink: sink, pseud: pseud, now: time.Now}
}

// Emit records that actor performed action on obj. The actor subject is
// resolved to its pseudonym token before the event is written.
func (e *Emitter) Emit(ctx context.Context, actor pii.Subject, action Action, obj Object, detail map[string]any) error {
	if action == "" || obj.Type == "" || obj.ID == "" {
		return fmt.Errorf("audit: event needs action, object type and id (got action=%q obj.Type=%q obj.ID=%q)", action, obj.Type, obj.ID)
	}
	token, err := e.pseud.TokenFor(ctx, actor)
	if err != nil {
		return err
	}
	return e.sink.Append(ctx, Event{
		ID:         id.V7(),
		Time:       e.now().UTC(),
		ActorToken: token,
		Action:     action,
		Object:     obj,
		Detail:     detail,
	})
}
