# ADR-0015: S3-compatible object storage, portable between providers

Status: Accepted · Date: 2026-10-27

## Context
Photos, audio practices, backups and exports need object storage inside the jurisdiction. Development and the pilot run on our own infrastructure; production may move to a local provider.

## Decision
- The code uses only the S3 API subset supported by all targets (put/get/delete, presigned URLs, multipart upload). No provider-specific features.
- Development and MVP: Garage (self-hosted). Production: any S3-compatible service located in the jurisdiction (for example a local cloud provider's object storage), chosen by configuration.
- Uploaded images are re-encoded server-side and stripped of metadata (EXIF/GPS) before storage; clients strip it before upload as well.
- Media safety scanning is an optional module behind a `MediaScanner` port. A scanner that sends media outside the jurisdiction is not allowed for user content; without a scanner, images go to human moderation before publication.

## Consequences
Bucket policies, lifecycle (retention) and encryption are configured per environment; backups use a separate bucket and credentials.
