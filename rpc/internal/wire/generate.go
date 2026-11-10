package wire

// Convenience source for go generate. The generator itself lives in ./gen
// and renders types.gen.go next to this file from the pinned execution-apis
// bundles. Run `go generate ./rpc/internal/wire/...` (or `go run ./gen` from
// this directory) after changing internal/spec data.
//
//go:generate go run ./gen -out types.gen.go
