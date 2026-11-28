package ethadapter

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Parse all production files, including files excluded by the current GOOS,
// so a platform-only import cannot silently bypass the root's active graph gate.
func TestAdapterProductionDependenciesAcrossPlatforms(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"github.com/codermaybe/ethguard":             true,
		"github.com/codermaybe/ethguard/ethapi":      true,
		"github.com/codermaybe/ethguard/rpc":         true,
		"github.com/ethereum/go-ethereum":            true,
		"github.com/ethereum/go-ethereum/common":     true,
		"github.com/ethereum/go-ethereum/core/types": true,
		"github.com/ethereum/go-ethereum/crypto":     true,
		"github.com/ethereum/go-ethereum/trie":       true,
		"github.com/ethereum/go-ethereum/ethclient":  true,
		"github.com/ethereum/go-ethereum/rpc":        true,
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Split(path, "/")[0], ".") {
				continue
			}
			if !allowed[path] {
				t.Errorf("%s imports unapproved dependency %s", entry.Name(), path)
			}
		}
	}
}
