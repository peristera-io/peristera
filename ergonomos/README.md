# Peristera Ergonomos

Tasks and projects — "the functionality of SharePoint with the UI of
Notion" (README §4). **M3b: the single-user task stub** — minimal but
pleasant, and the first app that stores user data, so it is the proof that
the four cross-cutting conventions compose: every task mutation flows
through personal-data metadata (`lib/pii`), authorization (`lib/authz`),
audit (`lib/audit`), and the search feed (`lib/search`).

Out of scope here (2027): multi-user/real-time, the Notion block editor
(Svelte + CRDT), emitted calendar entries.

**Status: M3b in progress.** Domain layer (`internal/task`) done and
unit-tested; Postgres stores, goose migrations, HTMX UI, and live
deployment follow.

License: AGPL-3.0-or-later with the Peristera App Store distribution
exception. Read the monorepo `README.md` first.

## Layout

```text
ergonomos/
├── README.md
├── internal/task/       ← the task domain, wired through the 4 conventions
├── internal/store/      ← Postgres implementations of the lib ports
├── migrations/          ← goose SQL (ADR-0014)
└── cmd/ergonomos/       ← boot: migrate, connect, serve
```

## Conventions it exercises

- **Personal-data metadata** — a `task` descriptor; export/erase a
  subject's tasks (ADR-0009).
- **OpenFGA** — an `owner` relation per task; listing and access go through
  `ListObjects`/`Check`, never a `WHERE owner =` (ADR-0010).
- **Audit** — every mutation emits a typed event, actor pseudonymized
  (ADR-0011).
- **Search** — every task feeds the FTS index on write (ADR-0012).
