// Package search implements the unified-search feed convention (ADR-0012):
// every app feeds a per-tenant Postgres full-text index on mutation. This
// is the write side only — the query surface (permission-filtered through
// OpenFGA) is deferred (ADR-0012 §4, issue #13). Feeding from the start is
// what makes cross-app search possible later without retrofitting.
package search

import (
	"context"
	"fmt"

	"github.com/peristera-io/peristera/lib/pii"
)

// Doc is a searchable record. It is derived data — rebuildable from the
// source entity — so erasure is "drop and let it rebuild" (ADR-0012 §5),
// after the source is erased (ADR-0009 §3).
type Doc struct {
	ID        string      // the source object's UUIDv7 (ADR-0007)
	Type      string      // namespaced type, e.g. "ergonomos/task"
	Permalink string      // canonical URL
	Owner     pii.Subject // for permission-filtering at query time
	Text      string      // the searchable content
}

// Index is the backing store (Postgres FTS). Upsert is idempotent on ID.
type Index interface {
	Upsert(ctx context.Context, d Doc) error
	Delete(ctx context.Context, id string) error
}

// Feeder is the app-facing write side of the search convention.
type Feeder struct {
	index Index
}

// NewFeeder builds a feeder over an index.
func NewFeeder(index Index) *Feeder {
	return &Feeder{index: index}
}

// Feed indexes (or re-indexes) a document on create/update. Owner and
// Permalink are required: without an owner the doc is unfilterable when
// permission-filtered querying lands (ADR-0012 §3) — an un-ownable doc
// would silently leak or vanish — and without a permalink a hit can't be
// linked. (The error avoids echoing Owner, which is personal data.)
func (f *Feeder) Feed(ctx context.Context, d Doc) error {
	switch {
	case d.ID == "" || d.Type == "":
		return fmt.Errorf("search: doc needs ID and Type (type=%q id=%q)", d.Type, d.ID)
	case d.Owner.Zero():
		return fmt.Errorf("search: doc %s/%s needs an Owner (for permission filtering)", d.Type, d.ID)
	case d.Permalink == "":
		return fmt.Errorf("search: doc %s/%s needs a Permalink", d.Type, d.ID)
	}
	return f.index.Upsert(ctx, d)
}

// Remove drops a document from the index on delete.
func (f *Feeder) Remove(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("search: Remove needs an id")
	}
	return f.index.Delete(ctx, id)
}
