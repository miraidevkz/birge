// Package openapi embeds the Birge HTTP OpenAPI document for runtime use.
package openapi

import _ "embed"

// Spec is the contents of birge.yaml (OpenAPI 3.1). Loaded once at process
// start by request validation (ADR-0005, ADR-0018).
//
//go:embed birge.yaml
var Spec []byte
