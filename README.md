# Birge

**Birge** («Бірге», Kazakh for "together") is an open-source platform of peer support and mental-health help for mothers and other caregivers of children with special educational needs. It starts in Kazakhstan, in Kazakh and Russian.

> Status: pre-alpha. Architecture and contracts are being written; there is no runnable code yet.

## The problem

Caring for a child with special needs often leaves the parent exhausted and isolated. Psychological support for parents is rarely covered by public services, and most families do not know which help and rights they have.

## What it does (MVP)

- **Check-ins:** a two-minute weekly well-being check (PHQ-4, PSS-4) with a trend chart and short audio practices.
- **AI assistant:** a supportive conversation in Kazakh and Russian, grounded only in a knowledge base reviewed by a psychologist and a lawyer. It does not diagnose or prescribe. A crisis protocol hands the person to a human, even when the AI is unavailable.
- **Mothers nearby:** matching with another mother from the same district for tea or a call; contacts are shared only with mutual consent.
- **SOS:** an urgent request to moderators and trusted contacts, an emergency plan with a child card, and a district SOS feed.
- **Sharing:** listings to give or swap items, and a directory of specialists with reviews.
- **Rights:** answers to common legal questions with links to the law.

## Principles

- Personal data stays in the country of the deployment; only anonymized text reaches the AI, with consent.
- The crisis path never depends on the AI.
- Secure by default (OWASP ASVS), FOSS dependencies only, built to be replicated in other countries by configuration.

## Documentation

- [Architecture](docs/architecture.md)
- [Architecture decision records](docs/adr/)
- [MVP requirements](docs/requirements/mvp.ru.md) (Russian)
- [Roadmap](docs/roadmap.md)
- [Threat model](docs/threat-model.md)
- [Security policy](SECURITY.md) · [Contributing](CONTRIBUTING.md) · [Code of conduct](CODE_OF_CONDUCT.md)
- [Contributing with AI agents](AGENTS.md)

## License

[Apache License 2.0](LICENSE). See [NOTICE](NOTICE).
