# Kamara ADR-0002 — Folder hierarchy

**Status:** accepted (M4b, Q&A Round 9, 2026-07-06)

## Context

M4a stored files as a flat, per-owner set. M4b adds a browsable file
experience: descend a tree, create folders, upload into a chosen location,
move and rename. This needs a hierarchy model that (a) keeps URLs stable
under moves (root ADR-0007: identity is the UUID, not a path), (b) fits the
OpenFGA-centric authorization (root ADR-0010) so folder *sharing* is a later
authorization addition rather than a data migration, and (c) does not block
per-file version history later.

## Decision

**Folders are first-class objects.** A `folders` row has a UUIDv7 id, an
owner subject, a nullable `parent_id` (NULL = a root-level folder), and a
name. Files gain a nullable `folder_id` (NULL = the owner's root). There is
no physical root row — the root is the implicit "NULL parent / NULL folder"
scope of one owner. Permalinks stay UUID-based (`/folders/{id}`,
`/files/{id}`); a move re-parents (changes `parent_id`/`folder_id`) and never
changes an id or URL.

**Authorization is `can_access`, inherited up the tree.** The OpenFGA model
gives `kamara/folder` and `kamara/file` each an `owner` (user), a `parent`
(folder), and a computed `can_access = owner OR can_access from parent`. On
create/move, Kamara writes the `owner` tuple and, when the item is inside a
folder, a `parent` tuple to that folder; access checks and listings use
`can_access`. Today every item is owned by the caller (per-owner trees), so
`can_access` reduces to `owner` — but the inheritance edges are real and
tested, so folder sharing later is a viewer tuple on a folder, inherited by
its subtree, with no model or schema change (#19).

**Deletion is empty-first.** A folder can be deleted only when it holds no
child folders or files; the domain enforces this and the FK columns are
`ON DELETE RESTRICT` as a backstop. Recursive delete is a later convenience.

## Consequences

- Move/rename are cheap metadata updates; content (chunks/blobs) is
  untouched and identity is stable.
- The authorization graph must be kept consistent with the folder tree:
  every create/move maintains the `parent` tuple alongside the `folder_id`/
  `parent_id` column. The tuple write is the same out-of-transaction seam as
  the owner tuple (root ADR-0015): DB first, tuple after — a crash can leave
  a re-parented row whose `parent` tuple lags, reconciled the same way as
  the owner-tuple seam.
- Cross-user sharing, recursive delete, and bulk/zip download are
  explicitly deferred; the model and schema already accommodate them.
- Cycle prevention (a folder moved under its own descendant) is a domain
  invariant, enforced by walking ancestors on move.
