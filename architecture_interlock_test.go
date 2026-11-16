package ethguard

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/codermaybe/ethguard"

// toolPackages are the evidence and probe layers: consumers of the core
// runtime, never dependencies of it (docs/design/method-verification-pipeline.md). cmd/* binaries
// are unimportable by construction, so only the internal tool packages need
// the ban.
var toolPackages = []string{
	modulePath + "/internal/livecanary",
	modulePath + "/internal/shapeprobe",
	modulePath + "/internal/rpcsample",
	modulePath + "/internal/rpcsample/replay",
}

// corePackages is the production core set the tool-import ban applies to:
// the facade and every package below it except the tools themselves.
var corePackages = map[string]bool{
	modulePath:                                 true,
	modulePath + "/rpc":                        true,
	modulePath + "/rpc/methods":                true,
	modulePath + "/pool":                       true,
	modulePath + "/aggregator":                 true,
	modulePath + "/config":                     true,
	modulePath + "/observe":                    true,
	modulePath + "/internal/runtime":           true,
	modulePath + "/internal/reference":         true,
	modulePath + "/internal/typed":             true,
	modulePath + "/internal/ethobject/header":  true,
	modulePath + "/internal/ethobject/receipt": true,
	modulePath + "/internal/ethobject/log":     true,
	modulePath + "/internal/transport":         true,
	modulePath + "/internal/transport/http":    true,
	modulePath + "/internal/transport/wss":     true,
	modulePath + "/internal/refcontext":        true,
	modulePath + "/internal/rpcwire":           true,
}

func banToolImports(t *testing.T, pkg packageImports, imports map[string]bool) {
	t.Helper()
	for _, tool := range toolPackages {
		if imports[tool] {
			t.Errorf("%s imports tool package %s; the core runtime is never a tool consumer's dependency", pkg.ImportPath, tool)
		}
	}
}

type packageImports struct {
	ImportPath string
	Imports    []string
}

// TestArchitectureDependencyInterlock keeps the topology's one-way package
// rules executable in CI. It inspects production Imports only; verification
// tests are allowed to consume production seams, while production code may
// not depend on verification or testkit.
func TestArchitectureDependencyInterlock(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", "./...")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(out)
	seen := 0
	rootUsesRuntime := false
	for {
		var pkg packageImports
		err := dec.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go list: %v", err)
		}
		seen++
		imports := make(map[string]bool, len(pkg.Imports))
		for _, imp := range pkg.Imports {
			imports[imp] = true
			if (strings.HasPrefix(imp, modulePath+"/internal/verification") ||
				strings.HasPrefix(imp, modulePath+"/internal/testkit")) &&
				!(pkg.ImportPath == modulePath+"/cmd/nativesmoke" && imp == modulePath+"/internal/testkit/gethoracle") {
				t.Errorf("production package %s imports verification-only package %s", pkg.ImportPath, imp)
			}
		}
		// Only the differential oracle may import ethclient. go-ethereum/rpc
		// itself stays confined to the oracle, the root test surface, and
		// cmd/* diagnostics; no production execution path links it.
		if imports["github.com/ethereum/go-ethereum/ethclient"] && pkg.ImportPath != modulePath+"/internal/testkit/gethoracle" {
			t.Errorf("%s imports ethclient; it is confined to the differential oracle", pkg.ImportPath)
		}
		if imports["github.com/ethereum/go-ethereum/rpc"] {
			switch {
			case pkg.ImportPath == modulePath+"/internal/testkit/gethoracle",
				pkg.ImportPath == modulePath,
				strings.HasPrefix(pkg.ImportPath, modulePath+"/cmd/"):
			default:
				t.Errorf("%s imports gethrpc; it is confined to the oracle, root typed facade, and cmd/*", pkg.ImportPath)
			}
		}

		// The core runtime is strictly downstream of no tool: tools and
		// evidence layers consume it (docs/design/method-verification-pipeline.md). A reverse edge
		// would let probe or canary concerns steer production behavior.
		if corePackages[pkg.ImportPath] {
			banToolImports(t, pkg, imports)
		}

		switch pkg.ImportPath {
		case modulePath:
			rootUsesRuntime = imports[modulePath+"/internal/runtime"]

			if imports[modulePath+"/internal/transport/http"] || imports[modulePath+"/internal/transport/wss"] {
				t.Error("root facade imports a concrete transport; the composition root assembles them in internal/runtime")
			}
		case modulePath + "/rpc":
			if imports[modulePath+"/rpc/methods"] {
				t.Error("rpc imports rpc/methods")
			}
		case modulePath + "/internal/reference":
			for banned := range map[string]string{
				modulePath + "/pool":             "pool",
				modulePath + "/rpc/methods":      "rpc/methods",
				modulePath + "/aggregator":       "aggregator",
				modulePath + "/internal/runtime": "internal/runtime",
			} {
				if imports[banned] {
					t.Errorf("internal/reference imports %s; reference owns only the head seam and the selection algorithms", banned)
				}
			}
		case modulePath + "/aggregator":
			if imports[modulePath+"/internal/runtime"] {
				t.Error("aggregator imports internal/runtime; runtime composes aggregator, never the reverse")
			}
		case modulePath + "/pool", modulePath + "/rpc/methods":
			for _, imp := range pkg.Imports {
				if strings.HasPrefix(imp, "github.com/ethereum/go-ethereum") {
					t.Errorf("%s imports %s; core packages stay on the transport Port", pkg.ImportPath, imp)
				}
			}
		case modulePath + "/internal/transport/wss":
			// The WSS adapter is a transport-only seam: it must not reach into
			// method plans, adjudication, reference establishment, or typed
			// projection.
			for banned := range map[string]string{
				modulePath + "/aggregator":         "aggregator",
				modulePath + "/rpc/methods":        "rpc/methods",
				modulePath + "/internal/runtime":   "internal/runtime",
				modulePath + "/internal/reference": "internal/reference",
				modulePath + "/internal/typed":     "internal/typed",
			} {
				if imports[banned] {
					t.Errorf("internal/transport/wss imports %s; WSS owns only the raw-call transport seam", banned)
				}
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("go list: %v", err)
	}
	if seen == 0 {
		t.Fatal("go list returned no packages")
	}
	if !rootUsesRuntime {
		t.Error("root facade does not use internal/runtime as its composition root")
	}
}

// TestDefaultBuildExcludesEthclient is the link-time gate: the default root
// dependency graph must not link the go-ethereum client, and neither the
// default build nor the WSS production path may reach a third-party websocket
// library on a production execution path — the owned wire stack replaced it.
// The public BlockNumberOrHash signature moved to the generated ethapi model,
// so no production package imports go-ethereum/rpc anymore; the gate below
// keeps guarding the historical surface: gorilla/websocket may appear in the
// default build only through go-ethereum's own packages, no EthGuard package
// may link it directly, and the WSS production path stays absolute.
func TestDefaultBuildExcludesEthclient(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".", "./internal/runtime").Output()
	if err != nil {
		t.Fatalf("go list -deps root/runtime: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "github.com/ethereum/go-ethereum/ethclient" {
			t.Fatal("the root/runtime dependency graph links ethclient")
		}
		if line == modulePath+"/internal/testkit/gethoracle" {
			t.Fatal("the root/runtime dependency graph links the differential oracle")
		}
		if line == "github.com/ethereum/go-ethereum/accounts/abi/bind" ||
			line == "github.com/ethereum/go-ethereum/accounts/abi/bind/v2" {
			t.Fatal("the default build links the abi/bind backend set; the bind/v2 view commitments are a test-only check")
		}
	}
	importers, err := gorillaImporters(".")
	if err != nil {
		t.Fatalf("inspect gorilla/websocket importers: %v", err)
	}
	for _, importer := range importers {
		if !strings.Contains(importer, "go-ethereum") {
			t.Fatalf("default build links gorilla/websocket from %s; only go-ethereum's own packages may carry it", importer)
		}
	}
	wire, err := exec.Command("go", "list", "-deps", "./internal/transport/wss", "./cmd/wsscapture").Output()
	if err != nil {
		t.Fatalf("go list -deps wss/capture: %v", err)
	}
	for _, line := range strings.Split(string(wire), "\n") {
		if line == "github.com/gorilla/websocket" {
			t.Fatal("the WSS production path still links gorilla/websocket")
		}
	}
	if _, err := os.Stat("gethcompat"); !os.IsNotExist(err) {
		if err != nil {
			t.Fatalf("inspect gethcompat: %v", err)
		}
		t.Fatal("public gethcompat package must not exist")
	}
}

// gorillaImporters names every package in the build graph of the given
// patterns that imports gorilla/websocket.
func gorillaImporters(pattern string) ([]string, error) {
	out, err := exec.Command("go", "list", "-deps", "-json", pattern).Output()
	if err != nil {
		return nil, err
	}
	var importers []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg packageImports
		if err := dec.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		for _, imp := range pkg.Imports {
			if imp == "github.com/gorilla/websocket" {
				importers = append(importers, pkg.ImportPath)
			}
		}
	}
	return importers, nil
}
