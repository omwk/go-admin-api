package gateway

import _ "embed"

// OpenAPI is the separately maintained M8a public contract.
//
//go:embed openapi.yaml
var OpenAPI []byte
