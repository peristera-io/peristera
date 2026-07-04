module github.com/peristera-io/peristera/iam

go 1.26

// lib is a sibling module in this monorepo, resolved locally.
require github.com/peristera-io/peristera/lib v0.0.0-00010101000000-000000000000

replace github.com/peristera-io/peristera/lib => ../lib

require (
	github.com/coreos/go-oidc/v3 v3.11.0 // indirect
	github.com/go-jose/go-jose/v4 v4.0.2 // indirect
	golang.org/x/crypto v0.25.0 // indirect
	golang.org/x/oauth2 v0.24.0 // indirect
)
