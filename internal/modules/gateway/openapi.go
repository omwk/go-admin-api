package gateway

import _ "embed"

// OpenAPI is the separate public contract for status calls and wallet operations.
//
//go:embed openapi.yaml
var OpenAPI []byte
