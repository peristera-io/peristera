# Peristera Kamara

File storage (Greek καμάρα, "vault"): chunked upload first, then sync.
Kamara exposes a **storage API** that other Peristera apps (e.g. Ergonomos)
consume as their file layer, and it stores user files behind the same
cross-cutting conventions every app follows (personal-data metadata,
OpenFGA authorization, audit, search). Deployed per tenant as a catalog
app with its own database, blob backend, and OpenFGA store.

**Status: M4 — not started.** Design in progress.

- **Design:** [`SPEC.md`](SPEC.md) — the living design document (what's
  decided, what's open). Read it first when working on Kamara.
- **Plan:** `docs/m4-plan.md` (milestone execution).
- **Decisions:** `adr/` (component-local ADRs, as they crystallize).

License: AGPL-3.0-or-later with the Peristera App Store distribution
exception (`LICENSE-EXCEPTION.md`). Read the monorepo `README.md` first —
it is the operating manual.
