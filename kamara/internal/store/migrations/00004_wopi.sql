-- +goose Up
-- M6 (ADR-0018): browser office editing via WOPI. Two additions, expand-only
-- (root ADR-0014).

-- #28: objects carry their MIME type so downloads and the WOPI GetFile serve a
-- correct Content-Type instead of a blanket application/octet-stream. Inferred
-- from the name on upload; empty means "unknown" (fall back to octet-stream).
ALTER TABLE objects ADD COLUMN content_type text NOT NULL DEFAULT '';

-- WOPI editing sessions. When a user opens a file in the office engine, Kamara
-- mints a per-session access token scoped to (file, user, permission, TTL).
-- Collabora presents it on every WOPI call; it is the whole security boundary
-- (Collabora publishes no proof-key — ADR-0018). We store only the token's
-- SHA-256 (like a password), never the token itself; access is additionally
-- re-checked against OpenFGA on every call, so a leaked token is bounded by
-- both the TTL and live authorization.
CREATE TABLE wopi_sessions (
    token_hash       text PRIMARY KEY,          -- sha256(token), hex
    object_id        text NOT NULL,             -- the file this session may touch
    subject_instance text NOT NULL,             -- the acting user (tenant + sub)
    subject_user     text NOT NULL,
    can_write        boolean NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL
);
-- Revoke-by-file (a session is dropped when its file is deleted) and the
-- opportunistic expiry sweep both scan by these.
CREATE INDEX wopi_sessions_object_idx ON wopi_sessions (object_id);
CREATE INDEX wopi_sessions_expiry_idx ON wopi_sessions (expires_at);

-- +goose Down
DROP TABLE wopi_sessions;
ALTER TABLE objects DROP COLUMN content_type;
