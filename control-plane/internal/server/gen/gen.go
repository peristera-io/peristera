// Package gen holds the oapi-codegen output for api/openapi.yaml.
// The spec is the source of truth; never edit api.gen.go by hand.
package gen

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 -config config.yaml ../../../api/openapi.yaml
