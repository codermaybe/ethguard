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

func inPackageTree(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// Production packages are core unless they belong to an explicit consumer
// tree. New helpers and subpackages inherit the diagnostic dependency ban
// without requiring another package-by-package registration.
func isCorePackage(path string) bool {
	if !inPackageTree(path, modulePath) {
		return false
	}
	for _, consumer := range []string{
		"/cmd", "/examples", "/rpctest", "/internal/diag", "/ethadapter",
		"/internal/testkit", "/internal/verification",
	} {
		if inPackageTree(path, modulePath+consumer) {
			return false
		}
	}
	return true
}

func banDiagnosticImports(t *testing.T, pkg packageImports, imports map[string]bool) {
	t.Helper()
	for imp := range imports {
		if inPackageTree(imp, modulePath+"/internal/diag") {
			t.Errorf("%s imports diagnostic package %s; core production packages must not depend on diagnostics", pkg.ImportPath, imp)
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
	for _, retired := range []string{
		"rpc/internal/objects", "rpc/internal/wire",
		"internal/livecanary", "internal/shapeprobe", "internal/rpcsample", "internal/chainlist",
	} {
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
		// The differential oracle and peripheral ethadapter may import
		// ethclient. gethrpc additionally serves cmd/* diagnostics; core
		// execution and transport packages cannot import either client.
		if imports["github.com/ethereum/go-ethereum/ethclient"] && pkg.ImportPath != modulePath+"/internal/testkit/gethoracle" && pkg.ImportPath != modulePath+"/ethadapter" {
			t.Errorf("%s imports ethclient; it is confined to the differential oracle and ethadapter", pkg.ImportPath)
		}
		if imports["github.com/ethereum/go-ethereum/rpc"] {
			switch {
			case pkg.ImportPath == modulePath+"/internal/testkit/gethoracle", pkg.ImportPath == modulePath+"/ethadapter",
				strings.HasPrefix(pkg.ImportPath, modulePath+"/cmd/"):
			default:
				t.Errorf("%s imports gethrpc; production imports are confined to ethadapter, the oracle and cmd/*", pkg.ImportPath)
			}
		}

		if inPackageTree(pkg.ImportPath, modulePath+"/ethadapter") {
			for imp := range imports {
				if inPackageTree(imp, modulePath+"/internal") {
					t.Errorf("adapter %s imports private package %s", pkg.ImportPath, imp)
				}
			}
		}
		// Diagnostics consume the core runtime. The whole diagnostic tree,
		// including future packages, is forbidden in core production imports.
		if isCorePackage(pkg.ImportPath) {
			banDiagnosticImports(t, pkg, imports)
			for imp := range imports {
				if inPackageTree(imp, modulePath+"/ethadapter") {
					t.Errorf("core package %s imports peripheral adapter %s", pkg.ImportPath, imp)
				}
			}
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
					"/internal/subscription",
				} {
					if imp == modulePath+engine || strings.HasPrefix(imp, modulePath+engine+"/") {
						t.Errorf("object package %s imports execution package %s", pkg.ImportPath, imp)
					}
				}
			}
		}

		switch pkg.ImportPath {
		case modulePath + "/ethadapter":
			for _, imp := range pkg.Imports {
				if !strings.Contains(strings.Split(imp, "/")[0], ".") {
					continue
				}
				switch imp {
				case modulePath, modulePath + "/ethapi", modulePath + "/rpc",
					"github.com/ethereum/go-ethereum", "github.com/ethereum/go-ethereum/common",
					"github.com/ethereum/go-ethereum/core/types", "github.com/ethereum/go-ethereum/crypto",
					"github.com/ethereum/go-ethereum/trie", "github.com/ethereum/go-ethereum/ethclient", "github.com/ethereum/go-ethereum/rpc":
				default:
					t.Errorf("ethadapter imports unapproved production dependency %s", imp)
				}
			}
		case modulePath + "/internal/endpoint":
			// Endpoint syntax and identity are shared data concerns. Keep the
			// parser independent of configuration, transports and execution.
			for _, imp := range pkg.Imports {
				if inPackageTree(imp, modulePath) || strings.Contains(strings.Split(imp, "/")[0], ".") {
					t.Errorf("endpoint parser imports non-standard dependency %s", imp)
				}
			}
		case modulePath:
			rootUsesRuntime = imports[modulePath+"/internal/runtime"]

			if imports[modulePath+"/internal/transport/http"] || imports[modulePath+"/internal/transport/wss"] || imports[modulePath+"/internal/transport/ipc"] {
				t.Error("root facade imports a concrete transport; the composition root assembles them in internal/runtime")
			}
		case modulePath + "/rpc":
			for _, execution := range []string{"rpcengine", "methods", "aggregator", "pool", "projection", "runtime", "reference", "subscription"} {
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
		case modulePath + "/internal/transport/streamrpc":
			for _, imp := range pkg.Imports {
				if !strings.Contains(strings.Split(imp, "/")[0], ".") {
					continue
				}
				switch imp {
				case modulePath + "/internal/rpcwire", modulePath + "/internal/transport", modulePath + "/rpc":
				default:
					t.Errorf("streamrpc imports %s; duplex RPC must depend only on its wire codec and neutral ports", imp)
				}
			}
		case modulePath + "/internal/transport/ipc":
			for _, imp := range pkg.Imports {
				if !strings.Contains(strings.Split(imp, "/")[0], ".") {
					continue
				}
				switch imp {
				case modulePath + "/internal/endpoint", modulePath + "/internal/jsonsafe", modulePath + "/internal/limits", modulePath + "/internal/transport", modulePath + "/internal/transport/streamrpc", "github.com/Microsoft/go-winio":
				default:
					t.Errorf("ipc imports %s; local framing must not reach execution or another transport", imp)
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
// Core production packages cannot import go-ethereum/rpc; the direct-import
// gate above permits only ethadapter, command diagnostics and the oracle.
// gorilla/websocket may appear in the default build through go-ethereum's own
// packages, but no EthGuard package may link it directly and the WSS production
// dependency graph must exclude it entirely.
func TestDefaultBuildExcludesEthclient(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".", "./internal/runtime", "./ethapi").Output()
	if err != nil {
		t.Fatalf("go list -deps root/runtime: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "github.com/ethereum/go-ethereum/ethclient" || inPackageTree(line, modulePath+"/ethadapter") {
			t.Fatal("the core dependency graph links ethclient or ethadapter")
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
	wire, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "./internal/transport/streamrpc", "./internal/transport/wss", "./cmd/wsscapture").Output()
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
