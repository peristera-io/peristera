package pii

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Descriptor declares, at the model level, that an entity type can hold
// personal data (ADR-0009 §1). The set of registered descriptors *is* the
// Article 30 processing registry (§5).
type Descriptor struct {
	// Type is the entity's namespaced type, matching its OpenFGA type and
	// permalink type where applicable, e.g. "ergonomos/task".
	Type string
	// Fields names the personal-data-bearing fields, for documentation and
	// the processing registry. Not load-bearing for export/erase — the
	// SubjectData hooks do the actual work — but required so "what personal
	// data is stored where" is answerable.
	Fields []string
	// Retention is the class governing this entity's data (default None).
	Retention RetentionClass
	// Hooks exports and erases this type's data for a subject. An app
	// provides one implementation per registered type.
	Hooks SubjectData
}

// SubjectData is the per-app, per-type export/erase contract (ADR-0009 §3).
// If ExportSubject can find it, EraseSubject can remove it.
type SubjectData interface {
	// ExportSubject returns this type's data relating to the subject in a
	// machine-readable form (nil if none).
	ExportSubject(ctx context.Context, s Subject) (any, error)
	// EraseSubject removes this type's data relating to the subject,
	// cascading per the type's metadata. It must respect retention/legal
	// holds (callers check BlocksErasureAt; hooks enforce their own cascade).
	EraseSubject(ctx context.Context, s Subject) error
}

// Descriptors are process-global by design: a type's personal-data shape
// (which fields, how to resolve the subject, retention class) is the same
// for every tenant, so it is registered once at init, not per tenant.
// (Per-tenant state — the pseudonym mapping — lives in a Pseudonyms
// instance instead.) regMu also guards the retention `classes` map.
var (
	regMu       sync.RWMutex
	descriptors = map[string]Descriptor{}
)

// Register records a descriptor. A type with personal data that is not
// registered cannot be exported or erased generically — so registration is
// the gate ADR-0009 relies on. Re-registering a type panics (a type has
// exactly one descriptor).
func Register(d Descriptor) {
	if d.Type == "" {
		panic("pii: descriptor needs a Type")
	}
	if d.Hooks == nil {
		panic("pii: descriptor " + d.Type + " needs Hooks")
	}
	if d.Retention.Name == "" {
		d.Retention = None
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := descriptors[d.Type]; dup {
		panic("pii: type already registered: " + d.Type)
	}
	descriptors[d.Type] = d
}

// Registry returns all descriptors, sorted by Type — the Article 30 view.
func Registry() []Descriptor {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Descriptor, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// ExportSubject gathers every registered type's data for a subject.
func ExportSubject(ctx context.Context, s Subject) (map[string]any, error) {
	out := map[string]any{}
	for _, d := range Registry() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := d.Hooks.ExportSubject(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("pii: export %s: %w", d.Type, err)
		}
		if data != nil {
			out[d.Type] = data
		}
	}
	return out, nil
}

// EraseSubject erases every registered type's data for a subject. Derived
// stores (search index) are dropped/rebuilt by the caller *after* this
// returns — the erasure ordering rule (ADR-0009 §3).
func EraseSubject(ctx context.Context, s Subject) error {
	for _, d := range Registry() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := d.Hooks.EraseSubject(ctx, s); err != nil {
			return fmt.Errorf("pii: erase %s: %w", d.Type, err)
		}
	}
	return nil
}
