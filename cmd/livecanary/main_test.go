package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/livecanary"
)

func commandProvenance() livecanary.Provenance {
	return livecanary.Provenance{
		Environment:  livecanary.Environment{Kind: "local", RunnerClass: "local"},
		SourceCommit: strings.Repeat("a", 40),
		GoVersion:    runtime.Version(),
		RunID:        "01JAZ3K4M5N6P7Q8R9S0T1V2WX",
		GeneratedAt:  time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
}

func testDependencies(provenance livecanary.Provenance) dependencies {
	return dependencies{
		loadSnapshot: livecanary.LoadSnapshot,
		detect:       func() (livecanary.Provenance, error) { return provenance, nil },
	}
}

func writeRegistrySnapshot(t *testing.T, dir string, raw []byte) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "raw.json"), raw, 0o644); err != nil {
		t.Fatalf("write raw registry: %v", err)
	}
	sum := sha256.Sum256(raw)
	snapshot := livecanary.Snapshot{
		SchemaVersion: livecanary.SnapshotSchemaVersion,
		URL:           "https://chainlist.org/rpcs.json",
		FetchedAt:     "2026-10-23T11:59:00Z",
		RawFile:       "raw.json",
		RawSHA256:     hex.EncodeToString(sum[:]),
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	path := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return path
}

func TestRunManifestWritesArtifactAndManifest(t *testing.T) {
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "livecanary", "testdata", "registry.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	snapshotPath := writeRegistrySnapshot(t, dir, raw)
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	if err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(commandProvenance())); err != nil {
		t.Fatalf("runManifest: %v", err)
	}

	manifestData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := livecanary.ParseManifest(manifestData)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Registry.Status != "ok" {
		t.Errorf("registry status = %q", manifest.Registry.Status)
	}
	if manifest.Registry.Redaction != livecanary.RedactionURLCredentials {
		t.Errorf("redaction = %q", manifest.Registry.Redaction)
	}
	if len(manifest.Selection.Selected) != 5 {
		t.Errorf("selected = %d, want 5", len(manifest.Selection.Selected))
	}
	artifactPath := filepath.Join(dir, livecanary.ArtifactName)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if bytes.Contains(artifact, []byte("SECRET")) || bytes.Contains(artifact, []byte("user:secret")) {
		t.Error("artifact leaked credentials")
	}
	sum := sha256.Sum256(artifact)
	if hex.EncodeToString(sum[:]) != *manifest.Registry.ArtifactSHA256 {
		t.Error("artifact digest does not match manifest")
	}
	output := stdout.String()
	if !strings.Contains(output, "manifest_sha256="+livecanary.ManifestSHA256(manifestData)) {
		t.Errorf("stdout does not report the manifest digest:\n%s", output)
	}
	if !strings.Contains(output, "registry_status=ok") {
		t.Errorf("stdout does not report registry status:\n%s", output)
	}
}

func TestRunManifestIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`[{"chainId":1,"rpc":["https://a.example","https://b.example","https://c.example"]}]`)
	snapshotPath := writeRegistrySnapshot(t, dir, raw)
	outPath := filepath.Join(dir, "manifest.json")
	for i := 0; i < 2; i++ {
		var stdout bytes.Buffer
		if err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(commandProvenance())); err != nil {
			t.Fatalf("runManifest: %v", err)
		}
	}
	first, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var stdout bytes.Buffer
	if err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(commandProvenance())); err != nil {
		t.Fatalf("runManifest: %v", err)
	}
	second, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("same snapshot and provenance produced different manifest bytes")
	}
}

func TestRunManifestUnavailableRegistry(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := writeRegistrySnapshot(t, dir, []byte(`{"not":"a registry"}`))
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	if err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(commandProvenance())); err != nil {
		t.Fatalf("runManifest: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := livecanary.ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Registry.Status != "unavailable" || manifest.Selection != nil {
		t.Errorf("unexpected unavailable manifest: %+v", manifest.Registry)
	}
	if _, err := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); !os.IsNotExist(err) {
		t.Error("unavailable registry must not produce a registry artifact")
	}
	if !strings.Contains(stdout.String(), "registry_status=unavailable") {
		t.Errorf("stdout does not report unavailable status:\n%s", stdout.String())
	}
}

func TestRunManifestRequiresFlags(t *testing.T) {
	var stdout bytes.Buffer
	if err := runWith([]string{"manifest", "-snapshot", "snapshot.json"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Error("runManifest accepted missing -out")
	}
	if err := runWith([]string{"manifest"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Error("runManifest accepted missing -snapshot")
	}
	if err := runWith(nil, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Error("run accepted no command")
	}
	if err := runWith([]string{"report"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Error("run accepted an unknown command")
	}
}

func TestRunManifestRejectsUnverifiedSnapshot(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`[]`)
	snapshotPath := writeRegistrySnapshot(t, dir, raw)
	if err := os.WriteFile(filepath.Join(dir, "raw.json"), []byte(`[{"chainId":1}]`), 0o644); err != nil {
		t.Fatalf("tamper raw: %v", err)
	}
	var stdout bytes.Buffer
	outPath := filepath.Join(dir, "manifest.json")
	err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(commandProvenance()))
	if err == nil {
		t.Fatal("runManifest accepted a raw registry that did not match raw_sha256")
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Error("failed verification still wrote a manifest")
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); !os.IsNotExist(statErr) {
		t.Error("failed verification still wrote an artifact")
	}
}
