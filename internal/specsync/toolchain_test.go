package specsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolchainDiscoveryDoesNotRequireBuildMachineGOROOT(t *testing.T) {
	workspace, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	installed, err := selectGoToolchain(t.Context(), workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOROOT", "")
	t.Setenv("PATH", filepath.Dir(installed.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOTOOLCHAIN", "go1.999.0")
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing-build-machine-go")} {
		got, err := discoverGoToolchain(t.Context(), workspace, "", root)
		if err != nil || got != installed {
			t.Fatalf("runtime GOROOT %q: got %+v, %v", root, got, err)
		}
	}
	got, err := discoverGoToolchain(t.Context(), workspace, installed.Path, "")
	if err != nil || got != installed {
		t.Fatalf("explicit installed toolchain: %+v, %v", got, err)
	}
	_, err = discoverGoToolchain(t.Context(), workspace, filepath.Join(t.TempDir(), "missing-explicit-go"), "")
	if err == nil || !strings.Contains(err.Error(), "-go-tool") {
		t.Fatalf("invalid explicit toolchain silently fell back: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := discoverGoToolchain(t.Context(), workspace, "", ""); err == nil {
		t.Fatal("missing installed toolchain did not fail")
	}
}
