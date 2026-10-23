package livecanary

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard"
)

func TestRerunUsesManifestBasis(t *testing.T) {
	fleet := newExecutorFleet(t, 3)
	fleet.scriptAgreeingFleet(t, 100)
	manifest := testManifest(t)
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	report, err := Rerun(context.Background(), manifestBytes, testExecutor(t, fleet.nodes, 1))
	if err != nil {
		t.Fatalf("Rerun: %v", err)
	}
	if report.State != StatePassed {
		t.Fatalf("state = %s", report.State)
	}
	if report.RunID != manifest.RunID || report.ManifestSHA256 != ManifestSHA256(manifestBytes) {
		t.Fatalf("report identity = run %s manifest %s", report.RunID, report.ManifestSHA256)
	}
	if len(report.Steps) != len(FixedPlan().Steps) {
		t.Fatalf("steps = %d", len(report.Steps))
	}
}

func TestRerunRejectsInvalidManifest(t *testing.T) {
	executor := Executor{NewClient: func(Manifest) (*ethguard.Client, error) {
		t.Fatal("invalid manifest must not build a client")
		return nil, nil
	}}
	if _, err := Rerun(context.Background(), []byte("{}"), executor); err == nil {
		t.Fatal("Rerun accepted an invalid manifest")
	}
}

func TestRerunUnavailableRegistryDoesNotBuildClient(t *testing.T) {
	manifest := testManifest(t)
	manifest.Registry = Registry{
		Status:    "unavailable",
		URL:       ChainlistURL,
		FetchedAt: "2026-10-23T12:00:00Z",
		Redaction: RedactionNone,
	}
	manifest.Selection = nil
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	executor := Executor{NewClient: func(Manifest) (*ethguard.Client, error) {
		t.Fatal("unavailable registry must not build a client")
		return nil, nil
	}}
	report, err := Rerun(context.Background(), manifestBytes, executor)
	if err != nil {
		t.Fatalf("Rerun: %v", err)
	}
	if report.State != StateInconclusive || !hasReason(report.StateReasons, "registry_unavailable", "") {
		t.Fatalf("state=%s reasons=%+v", report.State, report.StateReasons)
	}
	if len(report.Steps) != 0 {
		t.Fatalf("unavailable registry must not execute steps")
	}
}
