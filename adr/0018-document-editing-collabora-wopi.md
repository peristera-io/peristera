# ADR-0018: Browser document editing (Collabora + WOPI, opt-in per tenant)

- **Status:** accepted
- **Date:** 2026-07-06
- **Provenance:** Q&A Round 11 (R65–R70); `docs/m6-plan.md`; the M6 s0 spike
  (`hack/spike/`, findings below). Realises ADR-0004's "OnlyOffice / bootstrap"
  exception with a different engine; extends ADR-0013 (catalog) with an
  optional-per-tenant dimension. Refs #28.

## Context

M6 delivers browser document editing: a user opens a file stored in Kamara,
edits it in the browser, and saves — the edit lands back in Kamara as a new
version, owned by the user. An office editor is a decade of work, so ADR-0004
named it a *bootstrap* exception: integrate an existing engine behind a
document-service interface rather than build one. ADR-0004 tentatively named
**OnlyOffice**. Before committing we re-evaluated OnlyOffice vs **Collabora
Online**.

Two forces shaped the decision:

1. **Isolation.** The platform's whole pitch is per-tenant data isolation and
   encryption at rest. An editing engine decrypts document content to render
   it. A *shared* engine instance would mix multiple tenants' decrypted
   content in one process space — a real isolation risk against the premise,
   not a hypothetical one.
2. **Cost.** An office engine carries a genuine hardware cost (a LibreOffice
   process per open document). It should not tax the lean default footprint
   of tenants who never edit documents.

## Decision

**Engine: Collabora Online (CODE), deployed as an opt-in, per-tenant catalog
app — never a shared instance.** Integration is via **WOPI**, with the WOPI
host implemented **inside Kamara**. The engine stays behind the ADR-0004
document-service interface, so a later swap (e.g. to OnlyOffice for OOXML
fidelity) is contained.

### Why Collabora over OnlyOffice

| Axis | Collabora CODE | OnlyOffice CE |
|---|---|---|
| Footprint | `coolwsd` + per-doc chroot-jailed LibreOffice kits; no bundled DB/queue | bundles Postgres + RabbitMQ |
| License | MPL-2.0 | AGPL-3.0 |
| Auth model | per-session host-issued `access_token` (WOPI) | shared JWT secret across the instance |
| Connection cap | **unlimited by default** (see spike) | hard 20 editing connections in CE |
| Isolation | per-doc chroot jail | shared process |
| OOXML fidelity | good (LibreOffice) | **native** — OnlyOffice's one clear edge |

Collabora's WOPI model maps ~1:1 onto Kamara's existing OpenFGA-gated file
operations (`CheckFileInfo`/`GetFile`/`PutFile` ↔ get-metadata/download/
upload). OnlyOffice's only advantage — OOXML rendering fidelity — is kept
reachable by the swappable interface.

### Opt-in, per tenant, never shared

- A tenant enables the office engine through the catalog (mechanism in the
  ADR-0013 amendment below). Only then does the control plane provision a
  Collabora deployment **into that tenant's namespace**.
- Editor↔Kamara traffic is therefore **intra-tenant (same namespace)**, behind
  the ADR-0016 NetworkPolicy; no cross-tenant WOPI path exists to secure.
- It is priced as a premium/hardware feature, so its cost lands only on
  tenants who enable it. Tenants who don't get zero office footprint.

### The WOPI host lives in Kamara

Kamara owns the files, the OpenFGA authorization, and the version write, so it
serves the WOPI endpoints and the `/edit/{id}` page that embeds Collabora. No
new glue service.

- `GET  /wopi/files/{id}` → **CheckFileInfo** (metadata + permissions)
- `GET  /wopi/files/{id}/contents` → **GetFile** (decrypted bytes)
- `POST /wopi/files/{id}/contents` → **PutFile** (save-back = **new version**,
  owned by the acting user)

### WOPI auth: the access_token is the whole security boundary

Kamara mints a **per-session, opaque `access_token`** scoped to
(file, user, permissions, TTL) and validated against OpenFGA on every WOPI
call. Collabora presents it as an **`Authorization: Bearer`** header
(confirmed in the spike — coolwsd's own log: *"Specify access_token to set the
Authorization Bearer header"*).

**Collabora publishes no WOPI proof-key** (verified: absent from
`/hosting/discovery`; proof-keys are an MS-Office-Online feature CODE does not
implement). So R69's optional proof-key verification is **moot for Collabora** —
there is no second factor. The access_token therefore carries the entire
security boundary and must be correspondingly strong: short TTL, bound to a
single (file, user, permission set), single-use session, revocable, and
re-checked against OpenFGA on every call rather than trusted for its lifetime.

## Spike findings (M6 s0, verified on the dev cluster)

Collabora CODE `26.04.2.1` on k3d (arm64), against a stub WOPI host
(`hack/spike/`):

- **Deploys and runs.** Image ~512 MB; idle RSS ~460–480 MiB with one
  pre-forked kit. Needs `MKNOD/SYS_CHROOT/FOWNER/CHOWN/SYS_ADMIN` caps for the
  jails. Suggested per-tenant request ~640 MiB / 250m, limit 2 GiB / 2 CPU
  (revisit under real documents).
- **Connection cap.** Default config = **unlimited** concurrent connections
  and documents. The 20-connection / 10-document cap only applies if you opt
  into "home mode" (which suppresses the welcome/feedback popups). We keep the
  default (unlimited); this beats OnlyOffice CE's hard 20.
- **WOPI host allow-list.** coolwsd's default `storage.wopi` allow-list already
  permits cluster-private ranges (10/8, 172.16/12, 192.168/16, 127.0.0.1), so
  a same-namespace Kamara is accepted without extra config; production should
  still pin the exact host.
- **WS Origin enforcement.** coolwsd rejects the editor WebSocket upgrade
  (HTTP 403) unless its `Origin` is the Collabora host itself. The iframe embed
  and any reverse-proxy config must preserve this (a real integration
  constraint, not incidental).
- **Server-to-server round-trip (open path) proven end-to-end.** Driving a
  headless load over the cool WebSocket, coolwsd called back to our WOPI host
  for **CheckFileInfo** and **GetFile** over the cluster network under Cilium,
  and LibreOffice **opened the document** (`load success`, edit permission
  granted). This de-risks the network path, the allow-list, and the callback
  contract.
- **Save-back (PutFile) leg:** deferred to the s3 browser demo. Driving the
  edit+save leg over a *raw* WebSocket hit cool's internal view-init
  choreography (`nodocloaded`) — a synthetic-client artefact, not an
  architectural one; the browser bundle drives it normally. The PutFile
  contract is spec-stable (`POST .../contents`, `X-WOPI-Override: PUT`,
  `Authorization: Bearer`, body = full new bytes, expects `200` +
  `X-WOPI-ItemVersion`) and s2 builds Kamara's handler against it.

## Amendment to ADR-0004 (bootstrap exception)

ADR-0004's exceptions table named **OnlyOffice** as the document co-editing
bootstrap. That exception is now realised by **Collabora Online (CODE)** on the
same terms (integrated behind the document-service interface, not absorbed).
OnlyOffice remains a valid alternative engine behind the same interface should
OOXML fidelity later demand it.

## Amendment to ADR-0013 (catalog gains an optional-per-tenant dimension)

ADR-0013 kept the catalog as a Go slice of always-provisioned apps. M6 adds
the first **optional-per-tenant** app: the office engine is provisioned only
for tenants that enable it. The enablement signal is a per-tenant field on the
`Tenant` CR (e.g. `spec.apps: ["office"]`) that the reconciler reads; the
catalog stays code (a Go slice) and simply gains an "optional" flag plus this
per-tenant opt-in set. Full catalog-as-data (a CRD) stays deferred to the
MSP-curation trigger ADR-0013 already names.

## Consequences

- Kamara gains a WOPI host and a **version-write path** (M4 deferred this;
  save-back = a new version in the existing `versions` table, lighting up the
  stubbed Versions drawer). #28 (Content-Disposition/RFC 6266 + `fileType` on
  download) is now on the critical path — the engine fetches via `GetFile`.
- The control plane learns to provision a per-tenant Collabora deployment +
  Service + NetworkPolicy when the office app is enabled, and to tear it down
  when disabled.
- Security concentrates in the access_token (no proof-key fallback): it must be
  short-lived, tightly scoped, and re-authorized against OpenFGA per call.
- Co-editing is built to *work* (same WOPI document key) but the M6 DoD is
  single-user open→edit→save; multi-user polish is deferred, not dropped.

## Out of scope (deferred, not dropped)

Real-time co-editing polish; full version-history UI (diff/restore beyond a
list); OOXML-fidelity tuning / an OnlyOffice engine; spreadsheet/presentation
parity beyond "opens and saves"; the office app's own billing/metering (the
SaaS era). Each stays additive behind the document-service interface.
