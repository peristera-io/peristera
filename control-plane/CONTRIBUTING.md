# Contributing to Peristera Control Plane

Thanks for your interest! A few things to know before you start.

## How we work

Peristera Control Plane is developed with a specify → red → green → refactor (BDD) loop and
a written project memory. The **`README.md` is the operating manual for this
repository** — read its "Repository layout & how to work here" section first;
it applies to humans and LLM agents alike. In short:

- Specify behavior as a Gherkin `.feature` (domain/API level, run with
  godog), make it fail, then implement the smallest change to pass.
- After meaningful work, **append to `docs/worklog.md`**, add an **ADR**
  (`adr/`, or the project's own `adr/`) for non-obvious decisions, and add to
  **`guidelines/`** when a reusable convention emerges.
- Keep changes small and CI green.

## Contributor License Agreement (CLA)

Before we can accept your contribution, you must agree to the Peristera Control Plane
**Contributor License Agreement** (`CLA.md`).
Under it you **keep the copyright** to your work and grant the project a broad
license — including the right to relicense — so the project retains the
flexibility to change its license or dual-license in the future. See
**ADR-0005** for the reasoning, including the trade-off this asks of you.

**How to sign:** open your pull request as usual. A bot
([CLA Assistant](https://github.com/contributor-assistant/github-action))
checks whether you've signed. If you haven't, it comments with instructions —
you sign by replying on the PR with the exact phrase:

> I have read the CLA Document and I hereby sign the CLA

Your signature is recorded in `signatures/cla.json`. You sign only once; it
covers all your future contributions.

Contributing on behalf of an employer or other entity? Contact the maintainers
to arrange a **Corporate CLA** before submitting.

## Recognition

We keep a `CONTRIBUTORS.md` acknowledgements list. Feel free to add yourself in
your first pull request — it's separate from the CLA signature record, just a
thank-you.

## License

By contributing you agree that your contributions are licensed as set out in
the CLA — applications under AGPL-3.0-or-later **with** the App Store
distribution exception (`LICENSE`, `LICENSE-EXCEPTION.md`), libraries under
MIT.
