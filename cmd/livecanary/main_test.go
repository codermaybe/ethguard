package main

import (
	"bytes"
	"context"
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

func exampleReport(t *testing.T, input livecanary.RunInput) livecanary.Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "live-canary", "report.example.json"))
	if err != nil {
		t.Fatalf("read report example: %v", err)
	}
	report, err := livecanary.ParseReport(data)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	report.RunID = input.Manifest.RunID
	report.ManifestSHA256 = input.ManifestSHA256
	report.PlanSHA256 = input.Manifest.Plan.SHA256
	return *report
}

func TestRunLiveWritesManifestAndReport(t *testing.T) {
	dir := t.TempDir()
	deps := fetchDependencies(&commandTransport{
		status: http.StatusOK,
		body:   fetchRegistryBody("https://a.example", "https://b.example", "https://c.example"),
	}, commandProvenance())
	deps.runPlan = func(_ context.Context, input livecanary.RunInput) (livecanary.Report, error) {
		if input.Membership != livecanary.MembershipFixed {
			t.Fatalf("membership=%s, want fixed", input.Membership)
		}
		if got := livecanary.ManifestSHA256(input.ManifestBytes); got != input.ManifestSHA256 {
			t.Fatalf("manifest digest mismatch: %s != %s", got, input.ManifestSHA256)
		}
		return exampleReport(t, input), nil
	}
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	if err := runWith([]string{"run", "-out", outPath}, &stdout, deps); err != nil {
		t.Fatalf("run: %v", err)
	}
	manifestData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	reportPath := filepath.Join(dir, livecanary.ReportArtifactName)
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	report, err := livecanary.ParseReport(reportData)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if report.State != livecanary.StatePassed {
		t.Fatalf("state=%s", report.State)
	}
	if report.ManifestSHA256 != livecanary.ManifestSHA256(manifestData) {
		t.Fatal("report is not bound to the written manifest bytes")
	}
	if _, err := os.Stat(filepath.Join(dir, livecanary.ArtifactName)); err != nil {
		t.Fatalf("registry artifact: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"state=passed",
		"report_sha256=" + livecanary.ReportSHA256(reportData),
		"report=" + reportPath,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("stdout missing %q:\n%s", want, output)
		}
	}
}

func TestRunLiveRegistryFailureWritesInconclusiveReport(t *testing.T) {
	dir := t.TempDir()
	deps := fetchDependencies(&commandTransport{
		status: http.StatusInternalServerError,
		body:   []byte(`{"error":"unavailable"}`),
	}, commandProvenance())
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	err := runWith([]string{"run", "-out", outPath}, &stdout, deps)
	var inconclusive *inconclusiveError
	if !errors.As(err, &inconclusive) {
		t.Fatalf("err=%v, want inconclusive", err)
	}
	if exitCode(err) != 2 {
		t.Fatalf("exit=%d, want 2", exitCode(err))
	}
	reportData, readErr := os.ReadFile(filepath.Join(dir, livecanary.ReportArtifactName))
	if readErr != nil {
		t.Fatalf("read report: %v", readErr)
	}
	report, parseErr := livecanary.ParseReport(reportData)
	if parseErr != nil {
		t.Fatalf("ParseReport: %v", parseErr)
	}
	if report.State != livecanary.StateInconclusive || len(report.Steps) != 0 {
		t.Fatalf("unexpected report: state=%s steps=%d", report.State, len(report.Steps))
	}
	if !commandHasReason(report.StateReasons, "registry_unavailable") {
		t.Fatalf("reasons=%+v", report.StateReasons)
	}
	if !strings.Contains(stdout.String(), "state=inconclusive") {
		t.Fatalf("stdout: %s", stdout.String())
	}
}

func TestRunLiveInsufficientMembersIsInconclusive(t *testing.T) {
	dir := t.TempDir()
	deps := fetchDependencies(&commandTransport{
		status: http.StatusOK,
		body:   fetchRegistryBody("https://a.example", "https://b.example"),
	}, commandProvenance())
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	err := runWith([]string{"run", "-out", outPath}, &stdout, deps)
	var inconclusive *inconclusiveError
	if !errors.As(err, &inconclusive) {
		t.Fatalf("err=%v, want inconclusive", err)
	}
	reportData, readErr := os.ReadFile(filepath.Join(dir, livecanary.ReportArtifactName))
	if readErr != nil {
		t.Fatalf("read report: %v", readErr)
	}
	report, parseErr := livecanary.ParseReport(reportData)
	if parseErr != nil {
		t.Fatalf("ParseReport: %v", parseErr)
	}
	if !commandHasReason(report.StateReasons, "insufficient_members") {
		t.Fatalf("reasons=%+v", report.StateReasons)
	}
}

func TestRunLiveFailedStateUsesExitOne(t *testing.T) {
	dir := t.TempDir()
	deps := fetchDependencies(&commandTransport{
		status: http.StatusOK,
		body:   fetchRegistryBody("https://a.example", "https://b.example", "https://c.example"),
	}, commandProvenance())
	deps.runPlan = func(_ context.Context, input livecanary.RunInput) (livecanary.Report, error) {
		report := exampleReport(t, input)
		report.State = livecanary.StateFailed
		report.StateReasons = []livecanary.StateReason{{
			Category: "code_error",
			StepID:   nil,
			Detail:   "synthetic command failure",
		}}
		return report, nil
	}
	outPath := filepath.Join(dir, "manifest.json")
	var stdout bytes.Buffer
	err := runWith([]string{"run", "-out", outPath}, &stdout, deps)
	var failed *failedError
	if !errors.As(err, &failed) {
		t.Fatalf("err=%v, want failed", err)
	}
	if exitCode(err) != 1 {
		t.Fatalf("exit=%d, want 1", exitCode(err))
	}
	reportData, readErr := os.ReadFile(filepath.Join(dir, livecanary.ReportArtifactName))
	if readErr != nil {
		t.Fatalf("read report: %v", readErr)
	}
	report, parseErr := livecanary.ParseReport(reportData)
	if parseErr != nil {
		t.Fatalf("ParseReport: %v", parseErr)
	}
	if report.State != livecanary.StateFailed {
		t.Fatalf("state=%s", report.State)
	}
}

func TestRunLiveRequiresOut(t *testing.T) {
	var stdout bytes.Buffer
	if err := runWith([]string{"run"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Fatal("run accepted missing -out")
	}
	if err := runWith([]string{"run", "-out", "manifest.json", "extra"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Fatal("run accepted unexpected arguments")
	}
}

func TestRunVerifyReportsArtifactState(t *testing.T) {
	dir := t.TempDir()
	manifestData, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "live-canary", "manifest.example.json"))
	if err != nil {
		t.Fatalf("read manifest example: %v", err)
	}
	manifest, err := livecanary.ParseManifest(manifestData)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	artifact := []byte(`[{"chainId":1,"rpc":["https://rpc-a.example","https://rpc-b.example","https://rpc-c.example"]}]`)
	digest := livecanary.ManifestSHA256(artifact)
	manifest.Registry.RawSHA256 = &digest
	manifest.Registry.ArtifactSHA256 = &digest
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	reportData, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "live-canary", "report.example.json"))
	if err != nil {
		t.Fatalf("read report example: %v", err)
	}
	report, err := livecanary.ParseReport(reportData)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	report.ManifestSHA256 = livecanary.ManifestSHA256(manifestBytes)
	reportBytes, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for name, data := range map[string][]byte{
		livecanary.ManifestArtifactName: manifestBytes,
		livecanary.ReportArtifactName:   reportBytes,
		livecanary.ArtifactName:         artifact,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	var stdout bytes.Buffer
	if err := runWith([]string{"verify", "-dir", dir}, &stdout, testDependencies(commandProvenance())); err != nil {
		t.Fatalf("runWith verify: %v", err)
	}
	for _, want := range []string{
		"run_id=" + manifest.RunID,
		"state=passed",
		"manifest_sha256=" + livecanary.ManifestSHA256(manifestBytes),
		"report_sha256=",
		"registry_sha256=" + digest,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("verify output does not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunVerifyRequiresDirAndRejectsTampering(t *testing.T) {
	var stdout bytes.Buffer
	if err := runWith([]string{"verify"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Error("runVerify accepted a missing -dir")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, livecanary.ManifestArtifactName), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := runWith([]string{"verify", "-dir", dir}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Error("runVerify accepted an invalid manifest")
	}
}

func TestRunRerunWritesReportFromManifest(t *testing.T) {
	dir := t.TempDir()
	manifestData, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "live-canary", "manifest.example.json"))
	if err != nil {
		t.Fatalf("read manifest example: %v", err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := livecanary.ParseManifest(manifestData)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	deps := testDependencies(commandProvenance())
	deps.rerunPlan = func(_ context.Context, bytes []byte) (livecanary.Report, error) {
		report, err := livecanary.ParseReport(mustReadFile(t, filepath.Join("..", "..", "docs", "design", "live-canary", "report.example.json")))
		if err != nil {
			t.Fatalf("ParseReport: %v", err)
		}
		report.RunID = manifest.RunID
		report.ManifestSHA256 = livecanary.ManifestSHA256(bytes)
		report.PlanSHA256 = manifest.Plan.SHA256
		return *report, nil
	}
	reportPath := filepath.Join(dir, "report.json")
	var stdout bytes.Buffer
	if err := runWith([]string{"rerun", "-manifest", manifestPath, "-out", reportPath}, &stdout, deps); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	report, err := livecanary.ParseReport(reportData)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if report.RunID != manifest.RunID || report.ManifestSHA256 != livecanary.ManifestSHA256(manifestData) {
		t.Fatalf("report identity = run %s manifest %s", report.RunID, report.ManifestSHA256)
	}
	if !strings.Contains(stdout.String(), "state=passed") {
		t.Fatalf("stdout does not report passed:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, livecanary.ManifestArtifactName)); err != nil {
		t.Fatalf("rerun must leave the input manifest in place: %v", err)
	}
}

func TestRunRerunRequiresFlags(t *testing.T) {
	var stdout bytes.Buffer
	if err := runWith([]string{"rerun"}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Fatal("rerun accepted missing flags")
	}
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "missing.json")
	if err := runWith([]string{"rerun", "-manifest", manifestPath, "-out", filepath.Join(dir, "report.json")}, &stdout, testDependencies(commandProvenance())); err == nil {
		t.Fatal("rerun accepted a missing manifest")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func commandHasReason(reasons []livecanary.StateReason, category string) bool {
	for _, reason := range reasons {
		if reason.Category == category {
			return true
		}
	}
	return false
}
