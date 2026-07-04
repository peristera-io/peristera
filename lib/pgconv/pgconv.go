// Package pgconv holds the Postgres implementations of the cross-cutting
// convention stores every Peristera app shares: the pii pseudonym mapping,
// the append-only audit sink, and the search-feed index. Each app that
// stores user data (Ergonomos, Kamara, …) creates the same three tables
// (subject_pseudonyms, audit_events, search_documents) and wires these
// stores — so the SQL lives here once, over a dbtx.Executor so it runs
// inside or outside a transaction (root ADR-0015).
package pgconv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/dbtx"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
)

// --- pii.PseudonymStore ---

// PseudonymStore maps subject↔pseudonym token in `subject_pseudonyms`.
type PseudonymStore struct{ DB dbtx.Executor }

func (p *PseudonymStore) Lookup(ctx context.Context, s pii.Subject) (string, bool, error) {
	var tok string
	err := p.DB.QueryRowContext(ctx,
		`SELECT token FROM subject_pseudonyms WHERE instance=$1 AND user_id=$2`, s.Instance, s.UserID).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return tok, err == nil, err
}

// errSubjectMapped signals a lost allocation race so pii.TokenFor re-reads
// the winner (it only needs a non-nil error).
var errSubjectMapped = errors.New("pgconv: subject already mapped")

func (p *PseudonymStore) Save(ctx context.Context, token string, s pii.Subject) error {
	// ON CONFLICT DO NOTHING (not a bare INSERT) so a lost race doesn't
	// abort the surrounding transaction (Postgres 25P02); 0 rows affected =
	// another writer won → error so TokenFor re-reads the winner.
	res, err := p.DB.ExecContext(ctx,
		`INSERT INTO subject_pseudonyms (token, instance, user_id) VALUES ($1,$2,$3)
		 ON CONFLICT (instance, user_id) DO NOTHING`,
		token, s.Instance, s.UserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errSubjectMapped
	}
	return nil
}

func (p *PseudonymStore) Resolve(ctx context.Context, token string) (pii.Subject, bool, error) {
	var s pii.Subject
	err := p.DB.QueryRowContext(ctx,
		`SELECT instance, user_id FROM subject_pseudonyms WHERE token=$1`, token).Scan(&s.Instance, &s.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return pii.Subject{}, false, nil
	}
	return s, err == nil, err
}

func (p *PseudonymStore) Delete(ctx context.Context, s pii.Subject) error {
	_, err := p.DB.ExecContext(ctx,
		`DELETE FROM subject_pseudonyms WHERE instance=$1 AND user_id=$2`, s.Instance, s.UserID)
	return err
}

var _ pii.PseudonymStore = (*PseudonymStore)(nil)

// --- audit.Sink ---

// AuditSink appends to the per-tenant `audit_events` table (append-only).
type AuditSink struct{ DB dbtx.Executor }

func (a *AuditSink) Append(ctx context.Context, e audit.Event) error {
	var detail []byte
	if e.Detail != nil {
		var err error
		if detail, err = json.Marshal(e.Detail); err != nil {
			return err
		}
	}
	_, err := a.DB.ExecContext(ctx,
		`INSERT INTO audit_events (id, at, actor_token, action, object_type, object_id, object_permalink, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.ID, e.Time, e.ActorToken, string(e.Action),
		e.Object.Type, e.Object.ID, e.Object.Permalink, detail)
	return err
}

var _ audit.Sink = (*AuditSink)(nil)

// --- search.Index ---

// SearchIndex feeds the per-tenant `search_documents` FTS table.
type SearchIndex struct{ DB dbtx.Executor }

func (s *SearchIndex) Upsert(ctx context.Context, d search.Doc) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO search_documents (id, doc_type, permalink, owner_instance, owner_user, body)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO UPDATE SET doc_type=EXCLUDED.doc_type, permalink=EXCLUDED.permalink,
		   owner_instance=EXCLUDED.owner_instance, owner_user=EXCLUDED.owner_user, body=EXCLUDED.body`,
		d.ID, d.Type, d.Permalink, d.Owner.Instance, d.Owner.UserID, d.Text)
	return err
}

func (s *SearchIndex) Delete(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM search_documents WHERE id=$1`, id)
	return err
}

var _ search.Index = (*SearchIndex)(nil)

// SchemaSQL is the DDL for the three convention tables, so each app's
// migration creates identical tables. Apps embed this in a goose migration.
const SchemaSQL = `
CREATE TABLE subject_pseudonyms (
    token    text PRIMARY KEY,
    instance text NOT NULL,
    user_id  text NOT NULL,
    UNIQUE (instance, user_id)
);
CREATE TABLE audit_events (
    id               text PRIMARY KEY,
    at               timestamptz NOT NULL,
    actor_token      text NOT NULL,
    action           text NOT NULL,
    object_type      text NOT NULL,
    object_id        text NOT NULL,
    object_permalink text NOT NULL,
    detail           jsonb
);
CREATE TABLE search_documents (
    id             text PRIMARY KEY,
    doc_type       text NOT NULL,
    permalink      text NOT NULL,
    owner_instance text NOT NULL,
    owner_user     text NOT NULL,
    body           text NOT NULL,
    tsv            tsvector GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED
);
CREATE INDEX search_tsv_idx ON search_documents USING gin (tsv);
`
