# ADR-0005: Licensing model and relicensing CLA

- **Status:** accepted
- **Date:** 2026-07-02
- **Provenance:** pre-repo legal drafts; Q&A.md Rounds 2–3; README §7

## Context

Peristera must be credibly open source to win the Phase 0 self-hosting
audience, protected against competitors silently productizing it, and
flexible enough to fund itself in years 2–3 (SaaS, MSP program, possibly
commercial add-on modules). Licensing choices are nearly impossible to
reverse once there are many contributors — unless a CLA preserves the option.

## Decision

- **Applications:** AGPL-3.0-or-later (`LICENSE`) **with** the Peristera App
  Store distribution exception (`LICENSE-EXCEPTION.md`) so mobile clients can
  ship through app stores.
- **Libraries (`lib/`):** MIT (template: `templates/legal/LICENSE-MIT`).
- **Control plane:** open, AGPL, in the monorepo. Commercial add-on modules
  (billing integrations, fleet management, white-labeling) may live in a
  separate private repo in years 2–3, when they exist. Revisit at MSP alpha.
- **CLA (repo-wide, `CLA.md`):** contributors keep copyright and grant broad
  license including relicensing rights. Signed via CLA Assistant bot on the
  PR; recorded in `signatures/cla.json`. Repo-wide rather than per-project
  because the bot operates per GitHub repo; `templates/legal/` covers future
  splits.

## Consequences

- AGPL prevents closed-source competitive hosting of modifications; the
  exception keeps app stores viable; MIT keeps libraries maximally reusable.
- The relicensing CLA preserves open-core/dual-license optionality —
  **and creates a known tension:** copyleft-plus-relicensing-CLA is the
  pattern that makes some self-hosters and contributors decline to
  participate. We accept this consciously and answer it with transparency
  (stated in README §2 and §7), not by hiding it.
- The CLA is a template adapted from Harmony/Apache models and **must be
  reviewed by qualified legal counsel** (Luxembourg / EU author's-rights law)
  before the project relies on it in earnest.

## Alternatives considered

- **DCO instead of CLA:** lower contributor friction, but permanently forfeits
  relicensing flexibility — the funding optionality is worth more right now.
- **Copyright assignment:** stronger for the project, scares contributors far
  more than a license grant; rejected.
- **Permissive licensing (MIT/Apache) for apps:** invites closed-source
  competitive hosting; contradicts the sovereignty story.
