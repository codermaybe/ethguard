package specsync

import (
	"context"
	"encoding/json"
	"fmt"
	"go/version"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GoToolchain identifies an installed compiler, independently of the machine
// that built specsync. Release binaries built with -trimpath have no GOROOT.
type GoToolchain struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

func selectGoToolchain(ctx context.Context, workspace, explicit string) (GoToolchain, error) {
	root := runtime.GOROOT() //nolint:staticcheck // Optional discovery hint only; trimpath releases use PATH or -go-tool.
	return discoverGoToolchain(ctx, workspace, explicit, root)
}

func discoverGoToolchain(ctx context.Context, workspace, explicit, runtimeRoot string) (GoToolchain, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if runtimeRoot != "" {
			candidates = append(candidates, filepath.Join(runtimeRoot, "bin", "go"))
		}
		if path, err := exec.LookPath("go"); err == nil {
			candidates = append(candidates, path)
		}
	}
	problems := []string{}
	for _, candidate := range candidates {
		toolchain, err := probeGoToolchain(ctx, workspace, candidate)
		if err == nil {
			return toolchain, nil
		}
		problems = append(problems, err.Error())
	}
	return GoToolchain{}, fmt.Errorf("no usable installed Go toolchain; install the workspace-required Go version or set -go-tool (automatic downloads disabled): %s", strings.Join(problems, "; "))
}

func probeGoToolchain(ctx context.Context, workspace, candidate string) (GoToolchain, error) {
	path, err := exec.LookPath(candidate)
	if err != nil {
		return GoToolchain{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return GoToolchain{}, err
	}
	raw, err := command(ctx, workspace, true, path, "env", "-json", "GOROOT", "GOVERSION")
	if err != nil {
		return GoToolchain{}, err
	}
	var identity struct{ GOROOT, GOVERSION string }
	if err := json.Unmarshal(raw, &identity); err != nil {
		return GoToolchain{}, fmt.Errorf("invalid Go identity from %s: %w", path, err)
	}
	if !filepath.IsAbs(identity.GOROOT) || !version.IsValid(identity.GOVERSION) {
		return GoToolchain{}, fmt.Errorf("invalid installed Go identity from %s", path)
	}
	// Resolve wrappers/launchers to the concrete installed compiler. Probe it
	// again with local-only settings before recording and using its identity.
	installed := filepath.Join(identity.GOROOT, "bin", "go")
	verified, err := command(ctx, workspace, true, installed, "env", "GOVERSION")
	if err != nil {
		return GoToolchain{}, err
	}
	if strings.TrimSpace(string(verified)) != identity.GOVERSION {
		return GoToolchain{}, fmt.Errorf("installed Go launcher/compiler identity mismatch at %s", path)
	}
	return GoToolchain{Path: installed, Version: identity.GOVERSION}, nil
}
