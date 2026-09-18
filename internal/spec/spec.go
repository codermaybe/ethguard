// Package spec exposes the pinned execution-apis reference bundle: the
// per-method result contracts every comparison whitelist is compiled from.
//
// This package is reference data, not logic. Its only hand-written code is
// this accessor (unavoidable: go:embed must live next to the files). It
// imports nothing and decides nothing.
package spec

import (
	_ "embed"
	"strings"
)

//go:embed methods.json
var methodsJSON []byte

//go:embed VERSION
var version string

// MethodsJSON returns the vendored bundle: {"version": ..., "results":
// {method: resultSchema}}. Callers must treat it as read-only.
func MethodsJSON() []byte { return methodsJSON }

// Version returns the pinned execution-apis release (trimmed).
func Version() string { return strings.TrimSpace(version) }
