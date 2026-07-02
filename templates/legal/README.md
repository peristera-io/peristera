# Legal templates

Placeholder versions of the Peristera legal documents, for two situations:

1. **A project subfolder needs its own legal identity** (e.g. its README
   states its license explicitly): instantiate the relevant template into the
   project folder, replacing the placeholders.
2. **A component is split out of the monorepo** into its own repository
   (per ADR-0001, only when a real community forms around it): instantiate
   the full set, including the CLA — the repo-wide CLA in the monorepo root
   does not travel with a split automatically.

Placeholders:

- `{{PROJECT_NAME}}` — e.g. `Peristera Ergonomos`
- `{{LICENSE_NAME}}` — `AGPL-3.0-or-later with the Peristera App Store
  distribution exception` for applications, `MIT` for libraries
- `{{REPO_URL}}` — the repository the document lives in

The root of the monorepo carries the **live** documents (`LICENSE`, `CLA.md`,
`CONTRIBUTING.md`, `CONTRIBUTORS.md`, `LICENSE-EXCEPTION.md`) covering the
whole monorepo — see ADR-0005. The templates here are not themselves legally
operative.
