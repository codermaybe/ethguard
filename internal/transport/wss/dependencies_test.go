package wss

// The dependency interlock for the owned WSS wire layer. It is the executable
// form of wire_dependencies.md: production files may import only the Go
// standard library and the four listed project packages, the only third-party
// import allowed anywhere in production during the transition is the legacy
// adapter's gorilla binding in wss.go, and the link graph of this package must
// never reach the go-ethereum client or its rpc package. When W5 rewires
// wss.go onto the owned stack the transitional exception disappears on its
// own: any production third-party import then fails this test.

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/codermaybe/ethguard"

// allowedInternalImports enumerates the project packages the WSS adapter may
// import in production files. Anything else from the module is owned by an
// upper layer and must not be reachable from the transport seam.
var allowedInternalImports = map[string]string{
	modulePath + "/internal/limits":    "response-size bound shared with HTTP",
	modulePath + "/internal/rpcwire":   "project-owned JSON-RPC codec",
	modulePath + "/internal/transport": "transport Port and neutral sentinels",
	modulePath + "/rpc":                "hex-quantity parsing for ChainID",
}

// isStdlib reports whether an import path belongs to the Go standard library
// (no dot in the first path element).
func isStdlib(importPath string) bool {
	first := importPath
	if i := strings.IndexByte(importPath, '/'); i >= 0 {
		first = importPath[:i]
	}
	return !strings.Contains(first, ".")
}

// thirdPartyWebSocketLibraries names the libraries this package must never
// link. gorilla appears because it is the transitional binding being removed;
// the others must never appear at all.
var thirdPartyWebSocketLibraries = []string{
	"github.com/gorilla/websocket",
	"nhooyr.io/websocket",
	"github.com/coder/websocket",
	"golang.org/x/net/websocket",
}

func packageFileImports(t *testing.T, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var imports []string
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		imports = append(imports, path)
	}
	return imports
}

func TestWireLayerProductionImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, imp := range packageFileImports(t, name) {
			switch {
			case isStdlib(imp):
				continue
			case strings.HasPrefix(imp, modulePath+"/"):
				if _, ok := allowedInternalImports[imp]; !ok {
					t.Errorf("%s imports %s; the WSS seam owns only the raw-call transport layer (see wire_dependencies.md)", name, imp)
				}
			default:
				// The transitional exception: the legacy adapter file keeps
				// its gorilla binding until W5 rewires it. No other
				// production file, and no other third-party package, is
				// ever allowed.
				if name == "wss.go" && imp == "github.com/gorilla/websocket" {
					continue
				}
				t.Errorf("%s imports third-party package %s; production WSS code depends only on the standard library and project packages", name, imp)
			}
		}
	}
}

func TestNoThirdPartyWebSocketLibraryEntersTheWireLayer(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, imp := range packageFileImports(t, name) {
			for _, banned := range thirdPartyWebSocketLibraries {
				if imp == banned && name != "wss.go" {
					t.Errorf("%s imports %s; third-party websocket libraries stay out of production WSS code", name, imp)
				}
			}
		}
	}
}

func TestWireLayerLinkGraph(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps .: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch line {
		case "github.com/ethereum/go-ethereum/rpc", "github.com/ethereum/go-ethereum/ethclient":
			t.Errorf("the WSS package links %s; the transport seam must not reach the go-ethereum client", line)
		}
	}
}
