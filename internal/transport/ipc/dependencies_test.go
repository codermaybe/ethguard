package ipc

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestIPCProductionDependenciesStayAtTheWireBoundary(t *testing.T) {
	const module = "github.com/codermaybe/ethguard"
	allowed := map[string]bool{
		module + "/internal/endpoint": true, module + "/internal/jsonsafe": true, module + "/internal/limits": true,
		module + "/internal/transport": true, module + "/internal/transport/streamrpc": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.Contains(strings.Split(path, "/")[0], ".") || allowed[path] {
				continue
			}
			if path == "github.com/Microsoft/go-winio" && name == "dial_windows.go" {
				continue
			}
			t.Errorf("%s imports %s outside the IPC wire boundary", name, path)
		}
	}
}

func TestIPCPlatformFilesKeepWinioWindowsOnly(t *testing.T) {
	for _, target := range []struct {
		os, file string
		winio    bool
	}{{"linux", "dial_unix.go", false}, {"windows", "dial_windows.go", true}, {"plan9", "dial_other.go", false}} {
		t.Run(target.os, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "go", "list", "-json", ".")
			for _, item := range os.Environ() {
				if !strings.HasPrefix(item, "GOOS=") && !strings.HasPrefix(item, "GOARCH=") && !strings.HasPrefix(item, "CGO_ENABLED=") {
					cmd.Env = append(cmd.Env, item)
				}
			}
			cmd.Env = append(cmd.Env, "GOOS="+target.os, "GOARCH=amd64", "CGO_ENABLED=0")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list: %v", err)
			}
			var p struct{ GoFiles, Imports []string }
			if err := json.Unmarshal(out, &p); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, file := range p.GoFiles {
				if file == target.file {
					found = true
				}
			}
			if !found {
				t.Fatalf("platform dialer missing: %v", p.GoFiles)
			}
			winio := false
			for _, path := range p.Imports {
				if path == "github.com/Microsoft/go-winio" {
					winio = true
				}
			}
			if winio != target.winio {
				t.Fatalf("winio dependency=%v, want %v", winio, target.winio)
			}
		})
	}
}
