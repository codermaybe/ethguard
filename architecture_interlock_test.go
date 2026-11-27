package ethguard

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
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
	modulePath:                              true,
	modulePath + "/rpc":                     true,
	modulePath + "/internal/methods":        true,
	modulePath + "/internal/projection":     true,
	modulePath + "/internal/rpcengine":      true,
	modulePath + "/internal/pool":           true,
	modulePath + "/internal/aggregator":     true,
	modulePath + "/config":                  true,
	modulePath + "/observe":                 true,
	modulePath + "/internal/runtime":        true,
	modulePath + "/internal/reference":      true,
	modulePath + "/internal/typed":          true,
	modulePath + "/internal/subscription":   true,
	modulePath + "/internal/transport":      true,
	modulePath + "/internal/transport/http": true,
	modulePath + "/internal/transport/wss":  true,
	modulePath + "/internal/rpcwire":        true,
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
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// TestArchitectureDependencyInterlock keeps the topology's one-way package
// rules executable in CI. It inspects production Imports only; verification
// tests are allowed to consume production seams, while production code may
// not depend on verification or testkit.
func TestArchitectureDependencyInterlock(t *testing.T) {
	for _, retired := range []string{"rpc/internal/objects", "rpc/internal/wire"} {
		if _, err := os.Stat(retired); !os.IsNotExist(err) {
			t.Errorf("retired package tree %s must not exist (stat: %v)", retired, err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "list", "-json", "./...")
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
		if corePackages[pkg.ImportPath] || strings.HasPrefix(pkg.ImportPath, modulePath+"/internal/ethobject/") {
			banToolImports(t, pkg, imports)
		}

		// Objects own representation, integrity and request binding in one
		// tree. They may consume rpc data contracts, never the execution
		// engine or the facade that assembles it.
		if strings.HasPrefix(pkg.ImportPath, modulePath+"/internal/ethobject/") {
			for _, imp := range pkg.Imports {
				if imp == modulePath {
					t.Errorf("object package %s imports the root facade", pkg.ImportPath)
				}
				for _, engine := range []string{
					"/internal/methods", "/internal/aggregator", "/internal/pool",
					"/internal/runtime", "/internal/rpcengine", "/internal/projection",
				} {
					if imp == modulePath+engine || strings.HasPrefix(imp, modulePath+engine+"/") {
						t.Errorf("object package %s imports execution package %s", pkg.ImportPath, imp)
					}
				}
			}
		}

		switch pkg.ImportPath {
		case modulePath:
			rootUsesRuntime = imports[modulePath+"/internal/runtime"]

			if imports[modulePath+"/internal/transport/http"] || imports[modulePath+"/internal/transport/wss"] {
				t.Error("root facade imports a concrete transport; the composition root assembles them in internal/runtime")
			}
		case modulePath + "/rpc":
			for _, execution := range []string{"rpcengine", "methods", "aggregator", "pool", "projection", "runtime", "reference"} {
				if imports[modulePath+"/internal/"+execution] {
					t.Errorf("rpc data contracts import execution package %s", execution)
				}
			}
		case modulePath + "/internal/rpcengine":
			for _, owner := range []string{"methods", "aggregator", "pool", "projection", "runtime"} {
				if imports[modulePath+"/internal/"+owner] {
					t.Errorf("rpcengine imports its consumer %s", owner)
				}
			}
		case modulePath + "/internal/uncertain":
			if imports[modulePath+"/internal/rpcengine"] {
				t.Error("uncertain imports rpcengine; the collector satisfies the invocation capture port directly")
			}
		case modulePath + "/internal/subscription":
			// The owner orchestrates injected source capabilities. It may use
			// rpcengine and the neutral transport port, including in tests.
			allImports := append(append(append([]string(nil), pkg.Imports...), pkg.TestImports...), pkg.XTestImports...)
			for _, imp := range allImports {
				if imp == modulePath || strings.HasPrefix(imp, modulePath+"/internal/transport/") {
					t.Errorf("subscription imports connection implementation or facade %s", imp)
				}
				for _, implementation := range []string{"/pool", "/internal/pool", "/aggregator", "/internal/aggregator", "/rpc/methods", "/internal/methods", "/internal/runtime"} {
					if imp == modulePath+implementation || strings.HasPrefix(imp, modulePath+implementation+"/") {
						t.Errorf("subscription imports concrete implementation %s; inject source capabilities", imp)
					}
				}
			}
		case modulePath + "/internal/reference":
			for banned := range map[string]string{
				modulePath + "/internal/pool":       "pool",
				modulePath + "/internal/methods":    "internal/methods",
				modulePath + "/internal/aggregator": "aggregator",
				modulePath + "/internal/runtime":    "internal/runtime",
			} {
				if imports[banned] {
					t.Errorf("internal/reference imports %s; reference owns only the head seam and the selection algorithms", banned)
				}
			}
		case modulePath + "/internal/aggregator":
			if imports[modulePath+"/internal/runtime"] {
				t.Error("aggregator imports internal/runtime; runtime composes aggregator, never the reverse")
			}
		case modulePath + "/internal/pool", modulePath + "/internal/methods":
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
				modulePath + "/internal/aggregator": "aggregator",
				modulePath + "/internal/methods":    "internal/methods",
				modulePath + "/internal/runtime":    "internal/runtime",
				modulePath + "/internal/reference":  "internal/reference",
				modulePath + "/internal/typed":      "internal/typed",
				modulePath + "/internal/rpcengine":  "internal/rpcengine",
				modulePath + "/internal/projection": "internal/projection",
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
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".", "./internal/runtime").Output()
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
	importers, err := gorillaImporters(t, ".")
	if err != nil {
		t.Fatalf("inspect gorilla/websocket importers: %v", err)
	}
	for _, importer := range importers {
		if !strings.Contains(importer, "go-ethereum") {
			t.Fatalf("default build links gorilla/websocket from %s; only go-ethereum's own packages may carry it", importer)
		}
	}
	wire, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "./internal/transport/wss", "./cmd/wsscapture").Output()
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
func gorillaImporters(t *testing.T, pattern string) ([]string, error) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-json", pattern).Output()
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

func TestExecutionPackagesAreInternal(t *testing.T) {
	for _, retired := range []string{"aggregator", "pool", "rpc/methods", "rpc/projection"} {
		if _, err := os.Stat(retired); !os.IsNotExist(err) {
			t.Errorf("retired execution package %s remains: %v", retired, err)
		}
	}
}

func TestSubscriptionOwnerSurface(t *testing.T) {
	for _, name := range []string{"newhead_subscription.go", "logs_subscription.go", "receipts_subscription.go", "content_subscription.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok {
				t.Errorf("%s retains subscription behavior at root", name)
				continue
			}
			for _, spec := range group.Specs {
				if value, ok := spec.(*ast.TypeSpec); ok && !value.Assign.IsValid() {
					t.Errorf("%s owns type %s; public subscription types must alias their owner", name, value.Name.Name)
				}
			}
		}
	}
	for _, name := range []string{"logs_content.go", "logs_filter.go", "receipts_content.go"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Errorf("%s must belong to internal/subscription", name)
		}
	}
	entry, err := parser.ParseFile(token.NewFileSet(), "subscription.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range entry.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if method.Recv == nil || len(method.Recv.List) != 1 {
			t.Errorf("root subscription entry retains private helper %s", method.Name.Name)
			continue
		}
		receiver, ok := method.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			t.Errorf("unexpected subscription receiver for %s", method.Name.Name)
			continue
		}
		name, ok := receiver.X.(*ast.Ident)
		if !ok || name.Name != "Client" {
			t.Errorf("root owns subscription method %s", method.Name.Name)
		}
	}
}
