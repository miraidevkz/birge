# Contributing

Thank you for helping. Birge supports mothers of children with special needs, so correctness, privacy and safety come before speed.

## Before you start

1. Read [`AGENTS.md`](AGENTS.md). It applies to human contributors as well as AI agents: non-negotiables, module boundaries, security and database rules, definition of done.
2. Read [`docs/architecture.md`](docs/architecture.md) and the ADRs for the area you want to change.
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

UI strings live in locale files (`kk`, `ru`). Knowledge-base content is edited and reviewed in the staff admin, not in pull requests ([ADR-0022](docs/adr/0022-knowledge-base-workflow.md)).

## License

By contributing, you agree that your contribution is licensed under the [Apache License 2.0](LICENSE).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
