// Package push implements port.PushSender for the real push providers: FCM
// HTTP v1 for Android and APNs (token auth over HTTP/2) for iOS, plus Router,
// which picks one by the device platform.
//
// Only the standard library is used (ADR-0012 prefers it, and a provider SDK
// would pull in telemetry and a large dependency tree): the FCM OAuth2 token
// comes from a self-signed RS256 service-account JWT, and the APNs provider
// token is an ES256 JWT signed with the .p8 key.
//
// Invariants:
//   - The payload carries the notification ID and a neutral title only
//     (ADR-0013, AGENTS.md non-negotiable 3). Template, params and class never
//     leave the process: even the template key would tell Google or Apple
//     that, say, an SOS happened. Every push uses the same priority for the
//     same reason.
//   - Device tokens, OAuth tokens, JWTs and key material are never logged
//     and never appear in error texts. Errors carry the HTTP status and the
//     provider's reason code only.
//   - Provider errors are mapped onto port.ErrTokenInvalid (the service
//     removes the device), port.ErrPermanent (no retry) or a plain error
//     (the platform timer retries with backoff). This package never retries
//     by itself.
//   - Credentials are cached and refreshed under a mutex, so one sender is
//     safe for concurrent use by several timer workers.
package push
