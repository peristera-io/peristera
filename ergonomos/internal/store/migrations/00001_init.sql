-- +goose Up
-- Ergonomos initial schema (ADR-0014, expand-only). Four tables: the task
-- source, and the per-app stores for the pii pseudonym mapping, the
-- append-only audit log, and the search index.

CREATE TABLE tasks (
    id             text PRIMARY KEY,          -- UUIDv7 (ADR-0007)
    owner_instance text NOT NULL,             -- data subject (display only;
    owner_user     text NOT NULL,             --   authz is OpenFGA's)
    title          text NOT NULL,
    done           boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL
);
CREATE INDEX tasks_owner_idx ON tasks (owner_instance, owner_user);

-- Per-subject pseudonyms (ADR-0009 §7): audit references a token, and this
-- maps token↔subject. Erasing a subject deletes its row here, breaking
-- linkability while the append-only audit rows stay intact.
CREATE TABLE subject_pseudonyms (
    token    text PRIMARY KEY,
    instance text NOT NULL,
    user_id  text NOT NULL,
    UNIQUE (instance, user_id)                -- one token per subject
);

-- Append-only audit log (ADR-0011). Actor is a pseudonym token, never a
-- raw subject. (Grant-level append-only enforcement is a later hardening;
-- the lib/audit API is append-only.)
CREATE TABLE audit_events (
    id               text PRIMARY KEY,        -- UUIDv7
    at               timestamptz NOT NULL,
    actor_token      text NOT NULL,
    action           text NOT NULL,
    object_type      text NOT NULL,
    object_id        text NOT NULL,
    object_permalink text NOT NULL,
    detail           jsonb
);

-- Search index (ADR-0012): derived, rebuildable. tsv is generated from the
-- body for Postgres full-text search.
CREATE TABLE search_documents (
    id             text PRIMARY KEY,          -- the source object's id
    doc_type       text NOT NULL,
    permalink      text NOT NULL,
    owner_instance text NOT NULL,
    owner_user     text NOT NULL,
    body           text NOT NULL,
    tsv            tsvector GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED
);
CREATE INDEX search_tsv_idx ON search_documents USING gin (tsv);

-- +goose Down
DROP TABLE search_documents;
DROP TABLE audit_events;
DROP TABLE subject_pseudonyms;
DROP TABLE tasks;
