package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

type commandTransport struct {
	status int
	body   []byte
	count  int
}

func (t *commandTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.count++
	return &http.Response{
		StatusCode: t.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(t.body)),
	}, nil
}

func fetchRegistryBody(members ...string) []byte {
	quoted := make([]string, len(members))
	for i, member := range members {
		quoted[i] = `"` + member + `"`
	}
	return []byte(`[{"chainId":1,"rpc":[` + strings.Join(quoted, ",") + `]}]`)
}

func fetchDependencies(transport http.RoundTripper, provenance livecanary.Provenance) dependencies {
	deps := testDependencies(provenance)
	deps.newFetcher = func() livecanary.Fetcher {
		return livecanary.Fetcher{
			Transport: transport,
			Now: func() time.Time {
				return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			},
			Timeout: time.Second,
		}
	}
	return deps
}

func writeRegistrySnapshot(t *testing.T, dir string, raw []byte) string {
	t.Helper()
	return writeRegistrySnapshotAtURL(t, dir, raw, livecanary.ChainlistURL)
}

func writeRegistrySnapshotAtURL(t *testing.T, dir string, raw []byte, registryURL string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "raw.json"), raw, 0o644); err != nil {
		t.Fatalf("write raw registry: %v", err)
	}
	sum := sha256.Sum256(raw)
	snapshot := livecanary.Snapshot{
		SchemaVersion: livecanary.SnapshotSchemaVersion,
		URL:           registryURL,
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

func TestRunManifestRejectsNonChainlistSnapshotURL(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := writeRegistrySnapshotAtURL(t, dir, []byte(`[]`), "https://attacker.example/registry.json")
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	if err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Fatal("runManifest accepted a non-Chainlist snapshot URL")
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Error("rejected snapshot still wrote a manifest")
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); !os.IsNotExist(statErr) {
		t.Error("rejected snapshot still wrote an artifact")
	}
}

func TestRunManifestRejectsZeroGeneratedAt(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := writeRegistrySnapshot(t, dir, []byte(`[]`))
	outPath := filepath.Join(dir, "manifest.json")
	provenance := commandProvenance()
	provenance.GeneratedAt = time.Time{}
	var stdout bytes.Buffer
	if err := runWith([]string{"manifest", "-snapshot", snapshotPath, "-out", outPath}, &stdout, testDependencies(provenance)); err == nil {
		t.Fatal("runManifest accepted a zero generated_at")
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Error("zero generated_at still wrote a manifest")
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); !os.IsNotExist(statErr) {
		t.Error("zero generated_at still wrote an artifact")
	}
}

func TestRunFetchFixedMembership(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "manifest.json")
	transport := &commandTransport{
		status: http.StatusOK,
		body:   fetchRegistryBody("https://a.example", "https://b.example", "https://c.example"),
	}
	var stdout bytes.Buffer
	if err := runWith([]string{"fetch", "-out", outPath}, &stdout, fetchDependencies(transport, commandProvenance())); err != nil {
		t.Fatalf("runFetch: %v", err)
	}
	manifestData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := livecanary.ParseManifest(manifestData)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Registry.Status != "ok" || manifest.Selection == nil || len(manifest.Selection.Selected) != 3 {
		t.Errorf("unexpected manifest: status=%s selection=%+v", manifest.Registry.Status, manifest.Selection)
	}
	if _, err := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); err != nil {
		t.Errorf("artifact: %v", err)
	}
	if transport.count != 1 {
		t.Errorf("requests = %d, want 1", transport.count)
	}
	output := stdout.String()
	if !strings.Contains(output, "membership=fixed") {
		t.Errorf("stdout missing fixed membership:\n%s", output)
	}
	if strings.Contains(output, "state=inconclusive") {
		t.Errorf("fixed membership reported inconclusive:\n%s", output)
	}
}

func TestRunFetchInsufficientMembers(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "manifest.json")
	transport := &commandTransport{
		status: http.StatusOK,
		body:   fetchRegistryBody("https://a.example", "https://b.example"),
	}
	var stdout bytes.Buffer
	err := runWith([]string{"fetch", "-out", outPath}, &stdout, fetchDependencies(transport, commandProvenance()))
	var inconclusive *inconclusiveError
	if !errors.As(err, &inconclusive) {
		t.Fatalf("runFetch error = %v, want inconclusive", err)
	}
	if inconclusive.reason != string(livecanary.MembershipInsufficient) {
		t.Errorf("reason = %s", inconclusive.reason)
	}
	manifestData, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("insufficient membership must still write a manifest: %v", readErr)
	}
	manifest, parseErr := livecanary.ParseManifest(manifestData)
	if parseErr != nil {
		t.Fatalf("ParseManifest: %v", parseErr)
	}
	if manifest.Selection == nil || len(manifest.Selection.Selected) != 2 {
		t.Errorf("selected = %+v", manifest.Selection)
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); statErr != nil {
		t.Errorf("artifact: %v", statErr)
	}
	if !strings.Contains(stdout.String(), "state=inconclusive") ||
		!strings.Contains(stdout.String(), "membership=insufficient_members") {
		t.Errorf("stdout does not report inconclusive membership:\n%s", stdout.String())
	}
}

func TestRunFetchRegistryFailure(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "manifest.json")
	transport := &commandTransport{status: http.StatusInternalServerError, body: []byte(`{}`)}
	var stdout bytes.Buffer
	err := runWith([]string{"fetch", "-out", outPath}, &stdout, fetchDependencies(transport, commandProvenance()))
	var inconclusive *inconclusiveError
	if !errors.As(err, &inconclusive) {
		t.Fatalf("runFetch error = %v, want inconclusive", err)
	}
	if inconclusive.reason != string(livecanary.MembershipRegistryUnavailable) {
		t.Errorf("reason = %s", inconclusive.reason)
	}
	manifestData, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("registry failure must still write a manifest: %v", readErr)
	}
	manifest, parseErr := livecanary.ParseManifest(manifestData)
	if parseErr != nil {
		t.Fatalf("ParseManifest: %v", parseErr)
	}
	if manifest.Registry.Status != "unavailable" || manifest.Selection != nil {
		t.Errorf("unexpected unavailable manifest: %+v", manifest)
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); !os.IsNotExist(statErr) {
		t.Error("registry failure wrote an artifact")
	}
	if !strings.Contains(stdout.String(), "state=inconclusive") ||
		!strings.Contains(stdout.String(), "membership=registry_unavailable") {
		t.Errorf("stdout does not report registry failure:\n%s", stdout.String())
	}
}

func TestRunFetchMissingTargetChain(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "manifest.json")
	transport := &commandTransport{
		status: http.StatusOK,
		body:   []byte(`[{"chainId":2,"rpc":["https://other.example"]}]`),
	}
	var stdout bytes.Buffer
	err := runWith([]string{"fetch", "-out", outPath}, &stdout, fetchDependencies(transport, commandProvenance()))
	var inconclusive *inconclusiveError
	if !errors.As(err, &inconclusive) {
		t.Fatalf("runFetch error = %v, want inconclusive", err)
	}
	if inconclusive.reason != string(livecanary.MembershipRegistryUnavailable) {
		t.Errorf("reason = %s", inconclusive.reason)
	}
	manifestData, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("read manifest: %v", readErr)
	}
	manifest, parseErr := livecanary.ParseManifest(manifestData)
	if parseErr != nil {
		t.Fatalf("ParseManifest: %v", parseErr)
	}
	if manifest.Registry.Status != "ok" || manifest.Selection == nil || manifest.Selection.CandidateCount != 0 {
		t.Errorf("unexpected manifest: status=%s selection=%+v", manifest.Registry.Status, manifest.Selection)
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); statErr != nil {
		t.Errorf("target-chain failure should still publish registry bytes: %v", statErr)
	}
	if !strings.Contains(stdout.String(), "registry_status=ok") ||
		!strings.Contains(stdout.String(), "membership=registry_unavailable") {
		t.Errorf("stdout does not classify the missing target chain:\n%s", stdout.String())
	}
}

func TestRunFetchRejectsZeroGeneratedAtBeforeNetwork(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "manifest.json")
	transport := &commandTransport{status: http.StatusOK, body: fetchRegistryBody("https://a.example")}
	provenance := commandProvenance()
	provenance.GeneratedAt = time.Time{}
	var stdout bytes.Buffer
	if err := runWith([]string{"fetch", "-out", outPath}, &stdout, fetchDependencies(transport, provenance)); err == nil {
		t.Fatal("runFetch accepted a zero generated_at")
	}
	if transport.count != 0 {
		t.Errorf("requests = %d, want 0", transport.count)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Error("zero generated_at still wrote a manifest")
	}
	if _, statErr := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); !os.IsNotExist(statErr) {
		t.Error("zero generated_at still wrote an artifact")
	}
}

func TestRunFetchRequiresOut(t *testing.T) {
	var stdout bytes.Buffer
	if err := runWith([]string{"fetch"}, &stdout, fetchDependencies(&commandTransport{status: http.StatusOK}, commandProvenance())); err == nil {
		t.Error("runFetch accepted a missing -out")
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"failure", errors.New("boom"), 1},
		{"inconclusive", &inconclusiveError{reason: "insufficient_members"}, 2},
	}
	for _, test := range tests {
		if got := exitCode(test.err); got != test.want {
			t.Errorf("%s: exitCode = %d, want %d", test.name, got, test.want)
		}
	}
}
