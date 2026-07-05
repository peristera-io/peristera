-- +goose Up
-- Folder hierarchy (Kamara ADR-0002, expand-only per root ADR-0014).
-- Folders are first-class UUIDv7 objects with their own OpenFGA tuples. A
-- NULL parent_id is a root-level folder; a file's NULL folder_id means the
-- owner's root. Identity is the UUID and URLs carry it (ADR-0007), so a move
-- (re-parenting) never changes a folder's or file's permalink.

CREATE TABLE folders (
    id             text PRIMARY KEY,           -- UUIDv7
    owner_instance text NOT NULL,
    owner_user     text NOT NULL,
    parent_id      text REFERENCES folders(id) ON DELETE RESTRICT, -- NULL = root
    name           text NOT NULL,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL
);
CREATE INDEX folders_owner_idx ON folders (owner_instance, owner_user);
-- The FK column is not auto-indexed; listing a folder's children needs it.
CREATE INDEX folders_parent_idx ON folders (parent_id);

-- Files gain their location. NULL = the owner's root. RESTRICT: a folder
-- can't be deleted while it still holds files (the domain empties first).
ALTER TABLE objects ADD COLUMN folder_id text REFERENCES folders(id) ON DELETE RESTRICT;
CREATE INDEX objects_folder_idx ON objects (folder_id);

-- +goose Down
DROP INDEX objects_folder_idx;
ALTER TABLE objects DROP COLUMN folder_id;
DROP TABLE folders;
