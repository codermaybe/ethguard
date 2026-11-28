package ethguard

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Verify the actual read-only release gate with synthetic artifact bytes.
// No build, publication, active dist directory or network is involved.
func TestReleaseToolInventoryAndArtifactGate(t *testing.T) {
	config, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("scripts/release_build.sh")
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`readonly commands=\(([^)]+)\)`).FindSubmatch(script)
	if len(declaration) != 2 {
		t.Fatal("missing release command inventory")
	}
	commands := strings.Fields(string(declaration[1]))
	ids := regexp.MustCompile(`(?m)^  - id: ([a-z]+)$`).FindAllSubmatch(config, -1)
	builds := []string{}
	for _, id := range ids {
		if string(id[1]) != "binaries" {
			builds = append(builds, string(id[1]))
		}
	}
	if !slices.Equal(commands, builds) || !slices.Contains(commands, "specsync") {
		t.Fatalf("release build and verification inventories diverged: %v %v", commands, builds)
	}
	platforms := []string{"linux_amd64", "linux_arm64", "darwin_arm64"}
	if got := strings.Count(string(config), "targets: [linux_amd64, linux_arm64, darwin_arm64]"); got != len(commands) {
		t.Fatalf("platform targets missing: %d", got)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts/release_build.sh"), script, 0644); err != nil {
		t.Fatal(err)
	}
	var checksums strings.Builder
	for _, command := range commands {
		for _, platform := range platforms {
			name := command + "_" + platform
			raw := []byte("synthetic artifact: " + name)
			if err := os.WriteFile(filepath.Join(root, "dist", name), raw, 0644); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(raw), name)
		}
	}
	checksumPath := filepath.Join(root, "dist/checksums.sha256")
	if err := os.WriteFile(checksumPath, []byte(checksums.String()), 0644); err != nil {
		t.Fatal(err)
	}
	run := func() ([]byte, error) {
		cmd := exec.CommandContext(t.Context(), "bash", "scripts/release_build.sh", "--verify-artifacts")
		cmd.Dir = root
		return cmd.CombinedOutput()
	}
	output, err := run()
	if err != nil || !strings.Contains(string(output), fmt.Sprintf("(%d binaries)", len(commands)*len(platforms))) {
		t.Fatalf("complete candidate failed: %v %s", err, output)
	}
	if err := os.Remove(filepath.Join(root, "dist/specsync_linux_arm64")); err != nil {
		t.Fatal(err)
	}
	output, err = run()
	if err == nil || !strings.Contains(string(output), "specsync_linux_arm64") {
		t.Fatalf("missing specsync artifact passed: %v %s", err, output)
	}
}
