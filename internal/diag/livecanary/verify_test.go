package livecanary

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeVerifiedBundle(t *testing.T, manifest Manifest, artifact []byte, report Report) string {
	t.Helper()
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	reportBytes, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestArtifactName), manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ReportArtifactName), reportBytes, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if artifact != nil {
		if err := os.WriteFile(filepath.Join(dir, ArtifactName), artifact, 0o644); err != nil {
			t.Fatalf("write registry: %v", err)
		}
	}
	return dir
}

func passedBundle(t *testing.T) (string, Manifest, Report) {
	t.Helper()
	fleet := newExecutorFleet(t, 3)
	fleet.scriptAgreeingFleet(t, 100)
	artifact := []byte(`[{"chainId":1,"rpc":["https://rpc-a.example","https://rpc-b.example","https://rpc-c.example"]}]`)
	digest := ManifestSHA256(artifact)
	manifest := testManifest(t)
	manifest.Registry.RawSHA256 = &digest
	manifest.Registry.ArtifactSHA256 = &digest
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	report, err := testExecutor(t, fleet.nodes, 1).Run(context.Background(), RunInput{
		Manifest:       manifest,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: ManifestSHA256(manifestBytes),
		Membership:     MembershipFixed,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.State != StatePassed {
		t.Fatalf("state = %s", report.State)
	}
	return writeVerifiedBundle(t, manifest, artifact, report), manifest, report
}

func TestVerifyArtifactsAcceptsPublishedBundle(t *testing.T) {
	dir, manifest, report := passedBundle(t)
	verified, err := VerifyArtifacts(dir)
	if err != nil {
		t.Fatalf("VerifyArtifacts: %v", err)
	}
	if verified.RunID != manifest.RunID || verified.State != StatePassed {
		t.Fatalf("verified = %+v", verified)
	}
	if verified.ManifestSHA256 != report.ManifestSHA256 {
		t.Fatalf("manifest digest = %s, report binds %s", verified.ManifestSHA256, report.ManifestSHA256)
	}
	if verified.RegistrySHA256 == "" {
		t.Fatal("registry digest was not verified")
	}
}

func TestVerifyArtifactsRejectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, Manifest, Report)
	}{
		{"manifest bytes changed", func(t *testing.T, dir string, _ Manifest, _ Report) {
			appendFile(t, filepath.Join(dir, ManifestArtifactName), []byte("\n"))
		}},
		{"report digest changed", func(t *testing.T, dir string, _ Manifest, _ Report) {
			path := filepath.Join(dir, ReportArtifactName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read report: %v", err)
			}
			report, err := ParseReport(data)
			if err != nil {
				t.Fatalf("parse report: %v", err)
			}
			report.ManifestSHA256 = strings.Repeat("0", 64)
			writeReport(t, path, *report)
		}},
		{"run id changed", func(t *testing.T, dir string, _ Manifest, _ Report) {
			path := filepath.Join(dir, ReportArtifactName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read report: %v", err)
			}
			report, err := ParseReport(data)
			if err != nil {
				t.Fatalf("parse report: %v", err)
			}
			report.RunID = "01JAZ3K4M5N6P7Q8R9S0T1V2WY"
			writeReport(t, path, *report)
		}},
		{"registry bytes changed", func(t *testing.T, dir string, _ Manifest, _ Report) {
			appendFile(t, filepath.Join(dir, ArtifactName), []byte("\n"))
		}},
		{"registry missing", func(t *testing.T, dir string, _ Manifest, _ Report) {
			if err := os.Remove(filepath.Join(dir, ArtifactName)); err != nil {
				t.Fatalf("remove registry: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, manifest, report := passedBundle(t)
			test.mutate(t, dir, manifest, report)
			if _, err := VerifyArtifacts(dir); err == nil {
				t.Fatal("VerifyArtifacts accepted a tampered bundle")
			}
		})
	}
}

func appendFile(t *testing.T, path string, extra []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := file.Write(extra); err != nil {
		_ = file.Close()
		t.Fatalf("append %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func writeReport(t *testing.T, path string, report Report) {
	t.Helper()
	data, err := report.Marshal()
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}
