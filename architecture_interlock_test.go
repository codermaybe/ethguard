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
		switch pkg.ImportPath {
		case modulePath:
			rootUsesRuntime = imports[modulePath+"/internal/runtime"]
		case modulePath + "/rpc":
			if imports[modulePath+"/rpc/methods"] {
				t.Error("rpc imports rpc/methods")
			}
		case modulePath + "/ethadapter":
			if imports[modulePath+"/internal/runtime"] {
				t.Error("ethadapter imports internal/runtime; it must delegate through root facade")
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
