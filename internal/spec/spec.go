// Package spec exposes pinned, unmodified execution-apis source material.
// Reviewed corrections are assembled by internal/executionapi.
package spec

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed methods.json requests.json inventory.json SOURCE.json VERSION errata vectors
var bundleFS embed.FS

// FS returns the immutable spec root shared by the source assembler.
func FS() fs.FS { return bundleFS }
func sourceFile(name string) []byte {
	raw, err := bundleFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return raw
}
func MethodsJSON() []byte   { return sourceFile("methods.json") }
func RequestsJSON() []byte  { return sourceFile("requests.json") }
func InventoryJSON() []byte { return sourceFile("inventory.json") }
func SourceJSON() []byte    { return sourceFile("SOURCE.json") }
func ErrataFS() fs.FS       { return bundleFS }
func VectorsFS() fs.FS      { return bundleFS }
func Version() string       { return strings.TrimSpace(string(sourceFile("VERSION"))) }
