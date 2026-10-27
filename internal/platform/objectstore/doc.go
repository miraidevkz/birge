// Package objectstore is the S3-compatible object storage used for item
// photos and other in-jurisdiction blobs (ADR-0015). It talks only the
// portable S3 API subset (put/get/delete, presigned PUT/GET); no
// provider-specific calls. Development and the pilot use Garage; production
// may point at any S3-compatible service inside the jurisdiction.
//
// When configuration is unset, New returns a Disabled store: PresignPut and
// other mutating calls fail with ErrNotConfigured so photo uploads can
// answer 503 storage_not_configured, while the rest of the API keeps working.
package objectstore
