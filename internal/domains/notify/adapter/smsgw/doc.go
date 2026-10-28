// Package smsgw implements port.SMSSender for HTTP SMS gateways.
//
// HTTPSender owns everything providers share: the request lifecycle, a
// bounded response read, transport errors stripped of the request URL, and
// 429/5xx as retryable. A Provider only builds its request and reads its
// reply, so adding a second gateway is one file implementing Provider
// (SMSC is the first, for Kazakhstan).
//
// Invariants (ADR-0013, AGENTS.md non-negotiable 3):
//   - The text is the neutral fixed body notify already chose; this package
//     never adds to it.
//   - Phone numbers, texts and credentials are never logged and never appear
//     in error texts (the service logs errors). Errors carry the HTTP status
//     and the provider's numeric code only.
//   - Errors are mapped onto port.ErrPermanent (no retry) or a plain error
//     (the platform timer retries with backoff). There is no retry loop here.
package smsgw
