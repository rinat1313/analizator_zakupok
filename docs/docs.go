// Package docs embeds OpenAPI specification for the HTTP API.
package docs

import _ "embed"

// OpenAPI is the OpenAPI 3.0 YAML specification for analizator_zakupok.
//
//go:embed openapi.yaml
var OpenAPI []byte
