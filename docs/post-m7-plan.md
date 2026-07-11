# Post-M7 plan — reliability & custom-domain batch (R90–R96)

- **Status:** planning (2026-07-11). Parameters settled in `Q&A.md` Round 14
  (R90–R96), which resolved the cloud-infra reliability cluster (#52, #56, #43)
  and the optional-app lifecycle cluster (#63, #47, #48). Several facts were
  verified live on the running Scaleway node on 2026-07-08 (noted in `Q&A.md`).
  This plan dies when the batch ships.

## Goal

Close the load-bearing follow-ups left open at M7 completion: make first-provision
cert issuance deterministic, give custom-domain tenants a reversible attach flow
built self-serve-shaped (but still operator-only), let operators toggle optional
apps cleanly (create *and* delete), and harden the office engine for the public
node. `#43` (host-header bounce) is accepted and documented rather than fixed
(R92 → ADR-0016 amendment).

## Settled decisions (Q&A Round 14, 2026-07-11)

- **R90 — cert model.** Adopt the R77 decision **(A): Scaleway DNS-01 +
  per-tenant wildcard** (`*.<slug>.peristera.app`) for the platform domain, and
  **extend the same DNS-01 wildcard model to custom domains** via
  `_acme-challenge` **CNAME delegation** — the customer sets a one-time
  `_acme-challenge.<their-domain>` CNAME into a zone we control
  (`<slug>.acme.peristera.app`); cert-manager solves DNS-01 in our Scaleway zone
  and Let's Encrypt follows the CNAME. One cert story everywhere; retire the
  HTTP-01 per-host self-heal reconciler.
- **R90 — issuer/vanity decoupling.** The OIDC **issuer is permanent** and stays
  on the platform host; the **custom domain becomes a mutable, reversible
  app-routing attribute**, never the issuer. CRD consequence: `Tenant.spec.domain`
  stops being immutable-because-it-is-the-issuer. The issuer stays fixed.
- **R91 — #56 rescope.** Drop *dynamic external-dns zones* (customers own their
  DNS; external-dns keeps static `domainFilters` for Peristera-owned zones only)
  and drop *coredns-custom* (verified unnecessary). **Build domain-ownership
  verification now, operator-initiated** (TXT challenge gates domain attach).
  #56 becomes: (1) CNAME-delegation cert path, (2) ownership-verification
  mechanism, (3) `domain` as a reversible routing attribute. Self-serve UI +
  abuse controls stay in #53.
- **R92 — #43 host-header bounce.** **Accept + document** (option B). No L7 fix
  now; revisit with the zero-trust/token layer. Recorded as an ADR-0016
  amendment; #43 closed.
- **R93/R94 — optional-app lifecycle (#63, #47).** **Scoped reconcile for
  optional apps only:** converge the tenant's app resources to `spec.apps` —
  create on enable, **delete** on disable (label-selected
  Deployment/Service/Ingress/Certificate/NetworkPolicy), and rewire Kamara's env
  + `np-kamara` caller set on toggle. Does **not** reopen general
  drift-correction. API: **`PUT /tenants/{slug}/apps`** taking the full desired
  set (idempotent, validated against the catalog's Optional dimension); UI is a
  toggle on the tenant view. Toggling office rolls Kamara (a few-second blip).
- **R95 — office hardening (#48).** (1) keep `SYS_ADMIN` (coolwsd needs it for
  chroot jails) within the per-tenant namespace, add a **seccomp profile**,
  document; (2) **disable the admin console** rather than mint creds; (3)
  **gate prod-shaped settings on `tlsEnabled()`** (one dev/prod switch); (4) keep
  Traefik access logging off / redact `/wopi` if ever enabled — document, no code.
- **R96 — sequencing.** Cloud-infra cluster (#52, #56) first, verified live
  against the warm node; then the app-lifecycle cluster (#63/#47/#48), which is
  dev-cluster-testable; #43 is doc-only. Destroy the node after the cloud-infra
  part is verified.

## Delivery — reviewable PRs

Split for review; each code PR gets `/security-review` + `/code-review` and
updates the relevant ADRs/docs. Live-node verification (DNS-01 issuance, custom
domain cutover) is an operator step against the warm node.

1. **Decisions of record** (this PR) — R90–R96 answers in `Q&A.md`, this plan,
   the ADR-0016 amendment; closes #43.
2. **Office hardening** (R95) — `office.go` + docs; closes #48, #66. Dev-testable.
3. **Optional-app lifecycle** (R93/R94) — scoped reconcile + `PUT
   /tenants/{slug}/apps` + UI toggle + Kamara/netpol rewire; closes #63, #47.
   Dev-testable.
4. **Cert model + custom domains** (R90/R91) — DNS-01 wildcard issuance,
   issuer/vanity decoupling (CRD change + ADR), `_acme-challenge` CNAME
   delegation, ownership-verification state machine; closes #52, #56. **Needs
   live-node verification.**
