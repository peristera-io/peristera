# M4b / M4c plan — Kamara file UI + folder hierarchy

- **Status:** parameters settled (`Q&A.md` Round 9, 2026-07-06). Starts after
  M4a (done + live-verified). Split into **M4b** (hierarchy model + API +
  browser auth + minimal UI) and **M4c** (polish: extractable uploader,
  progress, details drawer, design tokens, a11y, demo) — Q&A R9 Q7.
- **Design home:** `kamara/SPEC.md` (living) + a new hierarchy ADR
  (`kamara/adr/0002-folder-hierarchy.md`). This plan is milestone execution
  and dies when M4 ships; the SPEC/ADRs persist.
- **Lifecycle:** working document, superseded by the M4 ADRs, the SPEC, and
  the worklog.

## Goal

A person browses, organizes, and moves files in Kamara through a pleasant
browser UI in the **Peristera design language** (Tailwind pilot): descend a
folder hierarchy, create folders, upload into a chosen location, download,
rename, move, delete. Built so three later features are **additive, not
rewrites**: an embeddable upload SDK, per-file version history, and
cross-user sharing.

## Decisions (Q&A Round 9, 2026-07-06)

- **Folders are first-class objects** (own UUID + OpenFGA tuples + `parent`),
  not a path string on files — so move/rename/share stay clean and URLs stay
  UUID-based (ADR-0007; a move never changes a permalink). New `folders`
  table; files gain a nullable `folder_id` (null = the owner's root). *(R9 Q1)*
- **Per-owner trees** for now: a file inherits its folder's access via an
  OpenFGA `parent` relation; **cross-user folder sharing deferred** (needs
  the OpenFGA sharing model #19 + the collaboration/S2S work). *(R9 Q2)*
- **Operation set:** browse tree, create folder, upload into the current
  folder, rename, move, delete (files + folders), single-file download.
  **Bulk/zip-folder download deferred.** *(R9 Q3)*
- **Tailwind, built at image-build time** with the standalone CLI (no Node
  in the pod); the CSS is `go:embed`ed into the binary. One Kamara
  `tailwind.config` with a small **named theme** (brand colour, font, radius)
  structured to become a shared preset later. Hand-written components (no
  component-kit dep). No hardcoded strings — the i18n catalog discipline
  (README §4). **Kamara is the Tailwind pilot; Ergonomos migrates later.**
  *(R9 Q4)*
- **File-details pullout drawer now** (name, size, created, location,
  permalink) with a **stubbed "Versions" section**; **no version history or
  new-version write path in M4b** (the `versions` table exists but only v0 is
  written — replace/version is its own later feature). M4b creates *new*
  files only. *(R9 Q5)*
- **Browser OIDC auth** (cookie session via `lib/oidcrp`) added to Kamara
  alongside the bearer API; both resolve to the same `pii.Subject`. *(R9 Q6)*

## Design principles carried in (the "don't block later" commitments)

- **SDK-ready.** `POST /v1/files` already returns a handle (UUID +
  permalink); content-addressing makes re-uploads near-free. The M4b/M4c
  uploader is built as a **self-contained, framework-agnostic component** —
  Kamara's own UI is merely its first consumer, so extraction into
  `@peristera/kamara-uploader` later is lift-and-shift. (Cross-app *auth* for
  a third-party embed still rides on the S2S milestone #29.)
- **Design-language-ready.** Tailwind + a named, extractable theme; the real
  shared design system is a later, separate effort — we only avoid choices
  that would fight it.
- **Version-ready.** The drawer ships a stubbed Versions section; the
  `versions`/`version_chunks` schema already supports history.
- **Sharing-ready.** Folders-as-objects + an OpenFGA `parent` relation mean
  sharing is a later authorization addition, not a data-model migration.

## M4b — sessions

| Session | Work |
|---|---|
| 1 | **Hierarchy backend.** `folders` table + `files.folder_id` (goose expand, ADR-0014); folder/file repos + domain on the ADR-0015 unit-of-work; OpenFGA `kamara/folder` type + `parent`→file inheritance (model accretion #19); the hierarchy ADR. API: folder CRUD, list a folder's children (folders + files), upload-into-folder, move, rename, single-file download — OpenFGA-authorized, wired through the four conventions. Unit tests + godog API coverage. |
| 2 | **Browser auth + Tailwind shell.** `lib/oidcrp` cookie login on Kamara (alongside the bearer API); the Tailwind build pipeline (standalone CLI in the image build → `go:embed` static CSS; named theme config); the base layout + the **file-browser page** (descend the tree, breadcrumb, list children) served via HTMX with the string catalog. |
| 3 | **File operations UI + live-verify.** Upload into the current folder, download, delete, create folder, rename, move — wired through HTMX. Live-verify in-cluster: log in → descend → create folder → upload into it → download → delete. |

### Definition of done (M4b)

- [x] Hierarchy model (folders + `folder_id`) migrated (expand); folder/file
      domain + repos; OpenFGA folder type + `parent` inheritance (#19); the
      hierarchy ADR; `kamara/SPEC.md` updated.
- [x] API for the full op set (folder CRUD, list children, upload-into,
      move, rename, download), OpenFGA-authorized, conventions-wired; godog
      API scenarios green (incl. cross-subject isolation on folders).
- [x] Browser OIDC auth on Kamara; bearer API and cookie UI share subject
      resolution.
- [x] Tailwind build pipeline + named theme; base layout + file-browser UI
      (browse / create-folder / upload / download / delete / rename / move),
      string catalog, no hardcoded strings.
- [x] Live-verified in-cluster end to end.

## M4c — sessions

| Session | Work |
|---|---|
| 4 | **Uploader component + drawer.** Extract the uploader into a self-contained, framework-agnostic component (Kamara UI is its first consumer): drag-drop + a **progress bar**. The **file-details pullout drawer** (metadata + location + permalink; a visibly-stubbed Versions section). |
| 5 | **Design tokens + a11y + demo.** Structure the theme for a future shared preset; the automated **a11y CI gate** (as Ergonomos); polish; the browser **demo** (descend tree, create folder, drag-drop upload with progress, open details drawer, download). |
| 6 | **Buffer + writing.** SPEC/README/worklog updates; demo recording. |

### Definition of done (M4c)

- [x] Uploader is a self-contained component (extractable to an SDK later);
      drag-drop + progress bar.
- [x] File-details pullout drawer (metadata + stubbed Versions section).
- [x] Design tokens structured for a shared preset; a11y CI gate green.
- [x] Demo: end-to-end browser flow. SPEC/README/worklog updated.

## Out of scope (deferred, not dropped)

Per-file **version history** + new-version write path; **cross-user folder
sharing**; **resumable/ranged** upload (planned — the uploader + API must
leave room, but not built); the actual **shared design-system** package;
the **Ergonomos attach flow** (moved to the S2S milestone #29, where it is
the acceptance test); **bulk/zip folder download**; content-extraction
search. Each is made additive by the principles above.
