# ADR-0013: Personal data stays in the jurisdiction

Status: Accepted · Date: 2026-10-27

## Context
Kazakhstan law requires personal data to be stored in databases located in the country. The LLM provider runs abroad (inference regions: US or global).

## Decision
All personal data, backups and logs stay in the deployment's jurisdiction. Only anonymized text goes to the LLM, and only with a separate cross-border consent. Third-party messengers are not used as channels; push and SMS carry no content.

## Consequences
Anonymization and its tests are on the critical path. A zero-data-retention agreement with the LLM provider is requested; models that require retention are not used for user content.
