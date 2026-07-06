# M6 plan — browser office editing (Collabora, opt-in per tenant)

- **Status:** direction settled (Collabora, opt-in per-tenant premium catalog
  app — 2026-07-06); parameters in `Q&A.md` Round 11 (pending answers). Runs
  after M5 (done). Renumber note: this is M6 (M5 = S2S, M7 = public demo).
- **Design home:** a new ADR for the document-editing integration
  (Collabora/WOPI + the opt-in catalog dimension) + amendments to ADR-0013
  (catalog gains optional-per-tenant apps) and the Kamara SPEC (WOPI host +
  version-write path). This plan dies when M6 ships; the ADRs persist.

## Goal

A user opens a document stored in Kamara, edits it in the browser via
Collabora Online, and saves — the edit lands back in Kamara **as a new
version**, owned by the user. Delivered as an **opt-in per-tenant premium
feature**: the office engine is a catalog app a tenant can enable (it carries
a real hardware cost), **never a shared instance** — so no tenant's document
content is ever processed alongside another's.

## Settled decisions (user, 2026-07-06)

- **Engine: Collabora Online (CODE)**, not OnlyOffice. Lighter (no bundled
  Postgres/RabbitMQ; `coolwsd` + per-doc chroot-jailed LibreOffice kit
  processes), MPL-2.0 (vs AGPL), and its **WOPI** model (host-issued
  per-session `access_token`) maps ~1:1 onto Kamara's existing OpenFGA-gated
  file operations. Kept behind the ADR-0004 document-service interface, so a
  later swap (e.g. for OOXML fidelity) is contained.
- **Opt-in, per tenant, never shared.** Mixing tenants' decrypted document
  content in one engine is a real isolation risk against the platform's whole
  premise; a per-tenant engine keeps editor↔Kamara traffic **intra-tenant
  (same namespace)** and isolation clean. It's a premium/hardware-priced
  feature (usage-based model), so its ~cost lands only on tenants who enable
  it — the lean default footprint is untouched.
- **The WOPI host lives in Kamara.** Kamara owns the files, the OpenFGA
  authorization, the signed access tokens, and the version write, so it
  serves the `CheckFileInfo` / `GetFile` / `PutFile` endpoints and the
  `/edit/{id}` page that embeds Collabora. No new glue service.
- **First optional catalog app.** M6 introduces the catalog's
  optional-per-tenant dimension (ADR-0013's named "catalog becomes data when
  an MSP curates" trigger).

## Open questions → `Q&A.md` Round 11

Catalog opt-in mechanism; Kamara version-write path (history vs replace);
co-editing scope for the DoD; Collabora deployment + WOPI auth specifics;
the `#28` Content-Disposition fold-in; CODE connection-cap verification.

## Sessions (provisional — finalized after Round 11)

| Session | Work |
|---|---|
| 0 | **Spike + ADR.** Deploy Collabora CODE on the dev cluster; verify a bare WOPI round-trip against a stub host (CheckFileInfo/GetFile/PutFile), the access-token + proof-key flow, and CODE's real connection behaviour. Write the editing-integration ADR. Confirm the deployment footprint. |
| 1 | **Catalog opt-in dimension.** The mechanism a tenant uses to enable the office app (ADR-0013 amendment); the control plane provisions Collabora into the tenant namespace only when enabled; NetworkPolicy for editor↔Kamara (same-namespace) + the browser. |
| 2 | **Kamara WOPI host + version-write path.** `CheckFileInfo`/`GetFile`/`PutFile` in Kamara, gated by OpenFGA + a per-session access token; the new-version write path (save-back = a new version; light up the stubbed Versions drawer); `#28` Content-Disposition/fileType. |
| 3 | **Editor UI + acceptance.** The `/edit/{id}` page embedding Collabora with a signed config; open → edit → save → reopen shows the change (a new version in Kamara). Live-verify in-cluster + a browser demo. |
| 4 | **Buffer + writing.** ADR/SPEC/README/worklog; demo. |

## Definition of done (provisional)

- [ ] Collabora CODE deployable per tenant, enabled via the catalog opt-in;
      not provisioned for tenants who don't enable it.
- [ ] Kamara serves the WOPI host (CheckFileInfo/GetFile/PutFile), authorized
      by OpenFGA + per-session access tokens; the save-back writes a new
      version of the file, owned by the user.
- [ ] A user opens a docx from Kamara, edits in Collabora, saves; reopening
      shows the change (verified in-cluster + a browser demo).
- [ ] Editing-integration ADR + ADR-0013 amendment + Kamara SPEC updated.

## Out of scope (deferred, not dropped)

Real-time multi-user co-editing polish (build so it *works*, don't gate on
it); full version-history UI (diff/restore beyond list); OnlyOffice/other
engines; spreadsheet/presentation parity beyond "it opens and saves"; the
office app's own billing/metering (the premium-pricing plumbing is the SaaS
era). Each stays additive behind the document-service interface.
