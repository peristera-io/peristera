# ADR-0007: Object identity, permalinks, and API versioning

- **Status:** accepted
- **Date:** 2026-07-03
- **Provenance:** deferred from M0 to "before the first URL/endpoint ships"
  (README §5); that is M2. Cross-cutting conventions, README §4.

## Context

SharePoint's broken-link hell is one URL-design decision made badly, once.
Peristera is API-first (the HTMX UI is just the first client), federated
(IDs cross instance boundaries), and GDPR-annotated at the schema level —
all three need stable object identity before the first endpoint exists.

## Decision

1. **Object identity is a UUIDv7**, generated at creation, immutable,
   unique per tenant. Time-ordered (index-friendly in Postgres), no
   semantic content — never derived from names, paths, or owners.
2. **URLs carry the ID; names are display-only.** Canonical permalink per
   type: `/{type}/{id}` (e.g. `/tasks/0198c…`). Renames and moves never
   change a URL. A human-readable slug may appear *after* the ID for
   cosmetics and is ignored by the server.
3. **Federated references are instance-namespaced:** a cross-instance
   subject or object is `{home-instance-domain}/{type}/{id}` — the tenant
   domain is permanent (ADR-0006), so these references are too.
4. **Tenant slugs are the one human-chosen identifier** (DNS label,
   immutable at creation) because they form the tenant domain = OIDC
   issuer. Display names change freely; slugs never.
5. **APIs are path-versioned per app: `/api/v1/…`** from the first
   endpoint. Within a version, changes are additive-only; breaking changes
   open `/api/v2` with a deprecation window. Until the first external
   consumer (public demo, M6), v1 may still break — each break logged in
   the worklog. Webhook payloads carry an explicit `schemaVersion`.
6. **Every API resource embeds its object ID and canonical permalink**, so
   clients never construct URLs from names.

## Consequences

- "Links never break" becomes testable: godog specs can assert a rename
  leaves the old permalink working.
- UUIDv7 everywhere means no natural keys in Postgres primary keys; lookups
  by name are explicit secondary indexes.
- Slug immutability must be enforced in the Tenant CRD (validation), not
  by convention.

## Alternatives considered

- **Path-based URLs with redirects on rename:** redirect chains rot and
  federation would ship mutable references. Rejected.
- **UUIDv4:** no index locality; v7 is the same size with better insert
  behavior. **Serial IDs:** enumerable, leak volume, collide on federation.
- **Header/content-negotiation API versioning:** harder to see in logs and
  curl, no benefit at our scale.
