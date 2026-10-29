# Contributing

Thank you for helping. Birge supports mothers of children with special needs, so correctness, privacy and safety come before speed.

## Before you start

1. Read [`AGENTS.md`](AGENTS.md). It applies to human contributors as well as AI agents: non-negotiables, module boundaries, security and database rules, definition of done.
2. Read [`docs/architecture.md`](docs/architecture.md), [`docs/development.md`](docs/development.md) and the ADRs for the area you want to change.
3. For anything bigger than a small fix, open an issue first and describe the change. A change that contradicts an ADR needs a new ADR.

Security problems are reported privately; see [`SECURITY.md`](SECURITY.md).

## Workflow

- Fork or branch from `main`: `<type>/<context>-<slug>`.
- Conventional Commits: `feat(safety): ...`, `fix(notify): ...`, `docs: ...`.
- Contracts first: a change to the HTTP API or events starts with a contract PR (`contracts/`).
- Keep PRs small and focused on one bounded context or app.
- Run `make check` before opening the PR and fill in the PR template.

## What we will not accept

- Dependencies that are not FOSS with an OSI-approved license, or that tie the project to a paid service ([ADR-0012](docs/adr/0012-foss-only-dependencies.md)).
- Code that sends personal data to third-party services or outside the deployment's jurisdiction ([ADR-0013](docs/adr/0013-data-residency.md)).
- Real personal data anywhere in the repository, including tests and screenshots.
- Changes that weaken the crisis path or its tests without sign-off from the project psychologist.

## Translations and content

UI strings live in locale files (`kk`, `ru`, `en`) and follow [`docs/content-style.md`](docs/content-style.md). Knowledge-base content is edited and reviewed in the staff admin, not in pull requests ([ADR-0022](docs/adr/0022-knowledge-base-workflow.md)).

## Writing documentation

Birge will be maintained by people who did not build it, so a change is not done until its documentation is. The [documentation map](docs/README.md) shows where each kind of page lives.

- **Update the doc in the same commit as the behaviour.** A changed endpoint, event, setting or rule updates the context README, the ADR (or a new one) and [`docs/development.md`](docs/development.md) where they describe it.
- **Lead with the point.** The first sentence of a page or section says what it is for or what the answer is; detail follows.
- **Write for a newcomer.** Short sentences, active voice, plain words. Define a project word once in the [glossary](docs/glossary.md) instead of explaining it on every page. No build history ("in the next wave", "the brief said"): say what is true now and why.
- **One fact, one place.** Link to the page that owns a fact rather than copying it; copies drift.
- **Comments say why.** Code comments explain the reason, the constraint or the failure behaviour, never what the next line does.
- `make docs-check` verifies every relative link and heading anchor; it also runs in CI, including for documentation-only changes.

## License

By contributing, you agree that your contribution is licensed under the [Apache License 2.0](LICENSE).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
