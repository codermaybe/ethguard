// Package spec exposes the pinned execution-apis reference bundle: the
// per-method result contracts every comparison whitelist is compiled from.
//
// This package is reference data, not logic. Its only hand-written code is
// this accessor (unavoidable: go:embed must live next to the files). It
// imports nothing and decides nothing.
package spec

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed methods.json
var methodsJSON []byte

//go:embed simulatev1/simulate.json
var simulateV1JSON []byte

//go:embed VERSION
var version string

//go:embed vectors
var vectors embed.FS

// MethodsJSON returns the vendored bundle: {"version": ..., "results":
// {method: resultSchema}}. Callers must treat it as read-only.
func MethodsJSON() []byte { return methodsJSON }

// SimulateV1JSON returns the vendored single-method bundle for
// eth_simulateV1: {"version", "method", "params", "result"}, produced by
// cmd/specmethod from the same official release as VERSION. The method is not
// part of methods.json because no conformance vectors for it are pinned here
// yet; the bundle exists so its official wire types can be code-generated
// without a geth dialect. Callers must treat it as read-only.
func SimulateV1JSON() []byte { return simulateV1JSON }

// Version returns the pinned execution-apis release (trimmed).
func Version() string { return strings.TrimSpace(version) }

// VectorsFS returns the embedded official .io conformance vectors. Callers
// must treat the returned filesystem as read-only.
func VectorsFS() fs.FS { return vectors }
