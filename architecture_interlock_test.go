package ethguard

import (
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/codermaybe/ethguard"

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
			if strings.HasPrefix(imp, modulePath+"/internal/verification") ||
				strings.HasPrefix(imp, modulePath+"/internal/testkit") {
				t.Errorf("production package %s imports verification-only package %s", pkg.ImportPath, imp)
			}
		}
		// The link-time transport rules: exactly one production package may
		// import the go-ethereum client, and the geth wire package stays
		// confined to the compatibility adapter, the ethclient-shaped facade,
		// and command entry points.
		if strings.Contains(pkg.ImportPath, "go-ethereum") {
			continue
		}
		importsETHClient := false
		for _, imp := range pkg.Imports {
			if imp == "github.com/ethereum/go-ethereum/ethclient" {
				importsETHClient = true
			}
		}
		if importsETHClient && pkg.ImportPath != modulePath+"/internal/transport/geth" {
			t.Errorf("%s imports ethclient; only internal/transport/geth may link the go-ethereum client", pkg.ImportPath)
		}
		importsGethRPC := false
		for _, imp := range pkg.Imports {
			if imp == "github.com/ethereum/go-ethereum/rpc" {
				importsGethRPC = true
			}
		}
		if importsGethRPC {
			switch {
			case pkg.ImportPath == modulePath+"/internal/transport/geth",
				pkg.ImportPath == modulePath+"/ethadapter",
				strings.HasPrefix(pkg.ImportPath, modulePath+"/cmd/"):
			default:
				t.Errorf("%s imports gethrpc; it is confined to internal/transport/geth, ethadapter, and cmd/*", pkg.ImportPath)
			}
		}

		switch pkg.ImportPath {
		case modulePath:
			rootUsesRuntime = imports[modulePath+"/internal/runtime"]
			if imports[modulePath+"/internal/transport/geth"] {
				t.Error("root facade imports the geth adapter; the compatibility channel is reachable only through gethcompat")
			}
			if imports[modulePath+"/internal/transport/http"] || imports[modulePath+"/internal/transport/wss"] {
				t.Error("root facade imports a concrete transport; the composition root assembles them in internal/runtime")
			}
		case modulePath + "/rpc":
			if imports[modulePath+"/rpc/methods"] {
				t.Error("rpc imports rpc/methods")
			}
		case modulePath + "/ethadapter":
			if imports[modulePath+"/internal/runtime"] {
				t.Error("ethadapter imports internal/runtime; it must delegate through root facade")
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
// default build nor the WSS production path may link a third-party websocket
// library — the owned wire stack replaced it. The compatibility channel is
// reachable only through gethcompat, whose own graph does link ethclient.
func TestDefaultBuildExcludesEthclient(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps .: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch line {
		case "github.com/ethereum/go-ethereum/ethclient":
			t.Fatal("the default build links ethclient; the geth channel must stay behind gethcompat")
		case "github.com/gorilla/websocket":
			t.Fatal("the default build links gorilla/websocket; the owned WSS wire stack replaced it")
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
	compat, err := exec.Command("go", "list", "-deps", "./gethcompat").Output()
	if err != nil {
		t.Fatalf("go list -deps ./gethcompat: %v", err)
	}
	linked := false
	for _, line := range strings.Split(string(compat), "\n") {
		if line == "github.com/ethereum/go-ethereum/ethclient" {
			linked = true
		}
	}
	if !linked {
		t.Fatal("gethcompat does not link the compatibility adapter it exists to expose")
	}
}
