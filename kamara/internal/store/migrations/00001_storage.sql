-- +goose Up
-- Kamara storage schema (Kamara ADR-0001, expand-only per root ADR-0014).
-- The convention tables (pseudonyms/audit/search) are added with their
-- wiring in a later migration.

-- Per-tenant format config, single row. The read/write/check feature flags
-- let an at-rest tenant later require an E2EE format and have old code
-- refuse cleanly rather than corrupt (Kamara ADR-0001 §4).
CREATE TABLE format_config (
    singleton        boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    format_version   int  NOT NULL,
    encryption_suite text NOT NULL,   -- e.g. "xchacha20poly1305"
    hash_algorithm   text NOT NULL,   -- e.g. "blake3-256"
    chunker          text NOT NULL,   -- e.g. "fastcdc-1m"
    features         jsonb NOT NULL    -- {"read":[...],"write":[...],"check":[...]}
);

-- Objects: a file. Identity is a UUIDv7 (root ADR-0007); the owner columns
-- are display / personal-data scoping only — authorization is OpenFGA's.
CREATE TABLE objects (
    id             text PRIMARY KEY,
    owner_instance text NOT NULL,
    owner_user     text NOT NULL,
    name           text NOT NULL,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL
);
CREATE INDEX objects_owner_idx ON objects (owner_instance, owner_user);

-- Versions: ordered snapshots of an object.
CREATE TABLE versions (
    id         text PRIMARY KEY,      -- UUIDv7
    object_id  text NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
    ordinal    int  NOT NULL,
    size       bigint NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (object_id, ordinal)
);

-- Chunks: content-addressed (hash of plaintext), ref-counted for
-- cross-version reuse + GC. Reserved E2EE columns are nullable at-rest
-- (Kamara ADR-0001 §5); the at-rest nonce lives in the blob header, not here.
CREATE TABLE chunks (
    hash              text PRIMARY KEY,   -- content address = storage key
    size              int  NOT NULL,
    ref_count         int  NOT NULL DEFAULT 0,
    wrapped_dek       bytea,
    origin_version_id text,
    origin_chunk_index int
);

-- version_chunks: the manifest — the positional binding (which chunk at
-- which index of which version), written inside the ADR-0015 transaction.
CREATE TABLE version_chunks (
    version_id text NOT NULL REFERENCES versions(id) ON DELETE CASCADE,
    idx        int  NOT NULL,
    chunk_hash text NOT NULL REFERENCES chunks(hash),
    PRIMARY KEY (version_id, idx)
);

-- +goose Down
DROP TABLE version_chunks;
DROP TABLE chunks;
DROP TABLE versions;
DROP TABLE objects;
DROP TABLE format_config;
