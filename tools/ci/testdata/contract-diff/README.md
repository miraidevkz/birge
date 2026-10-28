# Contract-diff fixtures

Self-test of `make contract-diff` (ADR-0018). `base.yaml` is a small OpenAPI 3.1 spec. Every `breaking-*.yaml` must be reported as breaking against it, every `compatible-*.yaml` must not; the target fails before it checks the real snapshots if either expectation breaks, so a tool upgrade that stops catching a breaking change cannot pass CI silently.
