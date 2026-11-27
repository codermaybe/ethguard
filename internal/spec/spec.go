// Package spec exposes the pinned execution-apis reference bundle: the
// per-method result contracts every comparison whitelist is compiled from.
//
// This package owns reference data and embed accessors. Schema assembly and
// reviewed errata enforcement belong to internal/executionapi; this package
// neither interprets schemas nor changes provider responses.
package spec

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed methods.json
var methodsJSON []byte

//go:embed requests.json
var requestsJSON []byte

//go:embed SOURCE.json
var sourceJSON []byte

//go:embed errata
var errata embed.FS

//go:embed VERSION
var version string

//go:embed vectors
var vectors embed.FS

// MethodsJSON returns the vendored bundle: {"version": ..., "results":
// {method: resultSchema}}. Callers must treat it as read-only.
func MethodsJSON() []byte { return methodsJSON }

// RequestsJSON returns unmodified parameter descriptors from the same pinned
// official OpenRPC source, independently of executable method coverage.
func RequestsJSON() []byte { return requestsJSON }

// SourceJSON returns the pinned upstream identity and reference bundle digests.
func SourceJSON() []byte { return sourceJSON }

// ErrataFS returns reviewed schema corrections, separate from upstream data.
func ErrataFS() fs.FS { return errata }

// Version returns the pinned execution-apis release (trimmed).
func Version() string { return strings.TrimSpace(version) }

// VectorsFS returns the embedded official .io conformance vectors. Callers
// must treat the returned filesystem as read-only.
func VectorsFS() fs.FS { return vectors }
