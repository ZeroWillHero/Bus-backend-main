// Package api embeds the OpenAPI contract so the server can serve it at
// /openapi.yaml without shipping a separate file.
package api

import _ "embed"

// Spec is the raw OpenAPI 3 document (openapi.yaml).
//
//go:embed openapi.yaml
var Spec []byte
