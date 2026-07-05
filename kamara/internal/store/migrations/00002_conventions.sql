-- +goose Up
-- The four cross-cutting convention tables (files are user data). These
-- match lib/pgconv.SchemaSQL exactly — the shared convention stores expect
-- these table shapes. Kept as literal DDL because goose migrations are SQL
-- files; keep in sync with lib/pgconv.SchemaSQL if that ever changes.

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

-- +goose Down
DROP TABLE search_documents;
DROP TABLE audit_events;
DROP TABLE subject_pseudonyms;
