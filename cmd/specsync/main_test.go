package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommandRejectsAmbiguousModesBeforeAcquisition(t *testing.T) {
	// Use the installed test toolchain directly, avoiding the PATH launcher's
	// implicit toolchain downloads in this offline CLI contract test.
	tool := filepath.Join(runtime.GOROOT(), "bin", "go") //nolint:staticcheck // The existing test toolchain is intentional for offline subprocesses.
	cmd := exec.CommandContext(t.Context(), tool, "run", ".", "-commit", strings.Repeat("a", 40), "-upstream-dir", t.TempDir(), "-out", filepath.Join(t.TempDir(), "candidate"))
	cmd.Env = append(cmd.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "select exactly one source input mode") {
		t.Fatalf("ambiguous CLI input was not rejected: %v %s", err, output)
	}
}
