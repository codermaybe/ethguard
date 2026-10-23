package livecanary

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixedPlanHashIsFrozen(t *testing.T) {
	plan := FixedPlan()
	got, err := ComputePlanSHA256(plan)
	if err != nil {
		t.Fatalf("ComputePlanSHA256: %v", err)
	}
	if got != FrozenPlanSHA256 {
		t.Fatalf("fixed plan hash = %s, want %s", got, FrozenPlanSHA256)
	}
	if err := plan.validateFixed(); err != nil {
		t.Fatalf("fixed plan validation: %v", err)
	}
}

func TestManifestExampleParses(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "design", "live-canary", "manifest.example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest(example): %v", err)
	}
	if manifest.Plan.SHA256 != FrozenPlanSHA256 {
		t.Errorf("example plan hash = %s", manifest.Plan.SHA256)
	}
	if got, err := ComputePlanSHA256(manifest.Plan); err != nil || got != FrozenPlanSHA256 {
		t.Errorf("example plan does not reproduce frozen hash: %s, %v", got, err)
	}
	if len(manifest.Selection.Selected) != 3 {
		t.Fatalf("example selected = %d, want 3", len(manifest.Selection.Selected))
	}
	wantBasis := "b39909215627518615f98e7ec3623e90d78cdaa32415ed5e491aa24373d7ff85"
	if manifest.Selection.VotingBasisSHA256 != wantBasis {
		t.Errorf("example voting basis = %s, want %s", manifest.Selection.VotingBasisSHA256, wantBasis)
	}
}

func TestBuildManifestValidAndDeterministic(t *testing.T) {
	result := buildFixture(t)
	if result.Manifest.Registry.Status != "ok" {
		t.Fatalf("registry status = %q", result.Manifest.Registry.Status)
	}
	if result.Manifest.Registry.Redaction != RedactionURLCredentials {
		t.Errorf("redaction = %q", result.Manifest.Registry.Redaction)
	}
	if len(result.Manifest.Selection.Selected) != 5 {
		t.Errorf("selected = %d, want 5", len(result.Manifest.Selection.Selected))
	}
	first, err := result.Manifest.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	second, err := result.Manifest.Marshal()
	if err != nil {
		t.Fatalf("Marshal again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("manifest serialization is not deterministic")
	}
	if !bytes.HasSuffix(first, []byte("\n")) {
		t.Error("manifest is missing a trailing newline")
	}
	if _, err := ParseManifest(first); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	sum := sha256.Sum256(first)
	if ManifestSHA256(first) != hex.EncodeToString(sum[:]) {
		t.Error("ManifestSHA256 does not match file bytes")
	}

	repeated, err := Build(BuildInput{
		Snapshot:   testSnapshot(readTestdata(t, "registry.json")),
		Raw:        readTestdata(t, "registry.json"),
		Provenance: testProvenance(),
		ChainID:    ChainID,
	})
	if err != nil {
		t.Fatalf("Build repeat: %v", err)
	}
	repeatedBytes, err := repeated.Manifest.Marshal()
	if err != nil {
		t.Fatalf("Marshal repeat: %v", err)
	}
	if !bytes.Equal(first, repeatedBytes) {
		t.Error("same inputs produced a different manifest")
	}
}

func TestManifestValidationRejectsMissingOrInconsistentProvenance(t *testing.T) {
	valid := buildFixture(t).Manifest
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"schema version", func(m *Manifest) { m.SchemaVersion = "ethguard.live-manifest/v2" }},
		{"run id", func(m *Manifest) { m.RunID = "" }},
		{"generated at", func(m *Manifest) { m.GeneratedAt = "2026-10-23T12:00:00+00:00" }},
		{"zero generated at", func(m *Manifest) { m.GeneratedAt = "0001-01-01T00:00:00Z" }},
		{"zero fetched at", func(m *Manifest) { m.Registry.FetchedAt = "0001-01-01T00:00:00Z" }},
		{"registry url", func(m *Manifest) { m.Registry.URL = "https://attacker.example/registry.json" }},
		{"source commit", func(m *Manifest) { m.SourceCommit = "abc123" }},
		{"tool version", func(m *Manifest) { m.ToolVersion = "unknown" }},
		{"go version", func(m *Manifest) { m.GoVersion = "1.25.3" }},
		{"local with ci", func(m *Manifest) {
			m.Environment.CI = &CIEnvironment{Provider: "github-actions"}
		}},
		{"ci without job", func(m *Manifest) {
			m.Environment = Environment{
				Kind:        "ci",
				RunnerClass: "github-hosted",
				CI: &CIEnvironment{
					Provider:    "github-actions",
					Repository:  "codermaybe/ethguard",
					WorkflowRef: "codermaybe/ethguard/.github/workflows/live-canary.yml@refs/heads/master",
					RunID:       "1",
					RunAttempt:  "1",
				},
			}
		}},
		{"chain id", func(m *Manifest) { m.ChainID = 5 }},
		{"registry status", func(m *Manifest) { m.Registry.Status = "partial" }},
		{"registry raw hash", func(m *Manifest) { m.Registry.RawSHA256 = nil }},
		{"registry redaction hash", func(m *Manifest) {
			m.Registry.Redaction = RedactionNone
		}},
		{"unavailable with artifact", func(m *Manifest) {
			m.Registry.Status = "unavailable"
		}},
		{"missing selection", func(m *Manifest) { m.Selection = nil }},
		{"selection algorithm", func(m *Manifest) { m.Selection.Algorithm = "chainlist-unsorted" }},
		{"selection source id", func(m *Manifest) { m.Selection.Selected[0].SourceID = "node-99" }},
		{"selection order", func(m *Manifest) {
			m.Selection.Selected[0], m.Selection.Selected[1] = m.Selection.Selected[1], m.Selection.Selected[0]
		}},
		{"candidate count", func(m *Manifest) { m.Selection.CandidateCount++ }},
		{"filter reason", func(m *Manifest) { m.Selection.Filtered[0].Reason = "unknown" }},
		{"filter hash", func(m *Manifest) { m.Selection.Filtered[0].URLSHA256 = "xyz" }},
		{"voting basis", func(m *Manifest) { m.Selection.VotingBasisSHA256 = FrozenPlanSHA256 }},
		{"selected non-https", func(m *Manifest) { m.Selection.Selected[0].URL = "http://alpha.example" }},
		{"selected credentials", func(m *Manifest) {
			m.Selection.Selected[0].URL = "https://user:pass@alpha.example"
		}},
		{"reference policy", func(m *Manifest) { m.Reference.Policy = "latest" }},
		{"reference lag", func(m *Manifest) { m.Reference.MaxLagBlocks = 0 }},
		{"timeout", func(m *Manifest) { m.Timeouts.StepMS = 1000 }},
		{"limits", func(m *Manifest) { m.Limits.RegistryMaxBytes = 1 }},
		{"plan id", func(m *Manifest) { m.Plan.ID = "other-plan" }},
		{"plan hash", func(m *Manifest) { m.Plan.SHA256 = FrozenPlanSHA256[:64] + "0" }},
		{"plan step", func(m *Manifest) { m.Plan.Steps[0].Method = "eth_blockNumber" }},
		{"plan step count", func(m *Manifest) { m.Plan.Steps = m.Plan.Steps[:5] }},
		{"plan param", func(m *Manifest) {
			m.Plan.Steps[3].Params[0] = 10
			recomputed, err := ComputePlanSHA256(m.Plan)
			if err != nil {
				t.Fatalf("recompute plan hash: %v", err)
			}
			m.Plan.SHA256 = recomputed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneManifest(t, valid)
			test.mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatalf("Validate accepted mutated manifest")
			}
		})
	}
}

func TestParseManifestIsStrict(t *testing.T) {
	data, err := buildFixture(t).Manifest.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	document["ai_filled"] = true
	withUnknown, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal unknown field: %v", err)
	}
	if _, err := ParseManifest(withUnknown); err == nil {
		t.Error("ParseManifest accepted an unknown field")
	}
	if _, err := ParseManifest(append(data, []byte("{}")...)); err == nil {
		t.Error("ParseManifest accepted trailing data")
	}
	delete(document, "ai_filled")
	delete(document, "source_commit")
	missing, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal missing field: %v", err)
	}
	if _, err := ParseManifest(missing); err == nil {
		t.Error("ParseManifest accepted a manifest without source_commit")
	}
}
