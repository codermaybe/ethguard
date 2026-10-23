package livecanary

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestBuildMarksUnparsableRegistryUnavailable(t *testing.T) {
	raw := []byte(`{"not":"a registry"}`)
	result, err := Build(BuildInput{
		Snapshot:   testSnapshot(raw),
		Raw:        raw,
		Provenance: testProvenance(),
		ChainID:    ChainID,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.RegistryError == nil {
		t.Error("expected a registry error")
	}
	if result.Artifact != nil {
		t.Error("unavailable registry must not publish an artifact")
	}
	if result.Manifest.Registry.Status != "unavailable" {
		t.Errorf("status = %q", result.Manifest.Registry.Status)
	}
	if result.Manifest.Selection != nil {
		t.Error("unavailable registry must not have a selection")
	}
	if result.Manifest.Registry.RawSHA256 != nil ||
		result.Manifest.Registry.Artifact != nil ||
		result.Manifest.Registry.ArtifactSHA256 != nil {
		t.Error("unavailable registry must publish null artifact fields")
	}
	data, err := result.Manifest.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	registry := document["registry"].(map[string]any)
	if registry["raw_sha256"] != nil || registry["artifact"] != nil || registry["artifact_sha256"] != nil {
		t.Error("unavailable artifact fields are not null in JSON")
	}
	if document["selection"] != nil {
		t.Error("selection is not null in JSON")
	}
}

func TestBuildRejectsProvenanceAndSnapshotMismatch(t *testing.T) {
	raw := readTestdata(t, "registry.json")
	tests := []struct {
		name   string
		mutate func(*BuildInput)
	}{
		{"raw hash", func(in *BuildInput) { in.Snapshot.RawSHA256 = sha256Hex([]byte("other")) }},
		{"snapshot url", func(in *BuildInput) { in.Snapshot.URL = "http://chainlist.org/rpcs.json" }},
		{"non-chainlist snapshot url", func(in *BuildInput) {
			in.Snapshot.URL = "https://attacker.example/registry.json"
		}},
		{"snapshot schema", func(in *BuildInput) { in.Snapshot.SchemaVersion = "v0" }},
		{"zero generated at", func(in *BuildInput) { in.Provenance.GeneratedAt = time.Time{} }},
		{"chain id", func(in *BuildInput) { in.ChainID = 5 }},
		{"run id", func(in *BuildInput) { in.Provenance.RunID = "" }},
		{"source commit", func(in *BuildInput) { in.Provenance.SourceCommit = "deadbeef" }},
		{"go version", func(in *BuildInput) { in.Provenance.GoVersion = "" }},
		{"ci without details", func(in *BuildInput) {
			in.Provenance.Environment = Environment{Kind: "ci", RunnerClass: "github-hosted"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := BuildInput{
				Snapshot:   testSnapshot(raw),
				Raw:        raw,
				Provenance: testProvenance(),
				ChainID:    ChainID,
			}
			test.mutate(&input)
			if _, err := Build(input); err == nil {
				t.Fatal("Build accepted missing or inconsistent provenance")
			}
		})
	}
}

func TestBuildWithoutCredentialsKeepsArtifactBytes(t *testing.T) {
	raw := []byte(`[{"chainId":1,"rpc":["https://a.example","https://b.example","https://c.example"]}]`)
	result, err := Build(BuildInput{
		Snapshot:   testSnapshot(raw),
		Raw:        raw,
		Provenance: testProvenance(),
		ChainID:    ChainID,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Manifest.Registry.Redaction != RedactionNone {
		t.Errorf("redaction = %q", result.Manifest.Registry.Redaction)
	}
	if !bytes.Equal(result.Artifact, raw) {
		t.Error("artifact differs from raw registry without credentials")
	}
	if *result.Manifest.Registry.RawSHA256 != *result.Manifest.Registry.ArtifactSHA256 {
		t.Error("raw and artifact hashes differ without redaction")
	}
}

func TestBuildEmptyTargetChainIsValidButIncomplete(t *testing.T) {
	raw := []byte(`[{"chainId":2,"rpc":["https://other.example"]}]`)
	result, err := Build(BuildInput{
		Snapshot:   testSnapshot(raw),
		Raw:        raw,
		Provenance: testProvenance(),
		ChainID:    ChainID,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Manifest.Registry.Status != "ok" {
		t.Errorf("status = %q, want ok", result.Manifest.Registry.Status)
	}
	if result.Manifest.Selection == nil || result.Manifest.Selection.CandidateCount != 0 {
		t.Errorf("expected an empty selection, got %+v", result.Manifest.Selection)
	}
	if len(result.Manifest.Selection.Selected) >= MinMembers {
		t.Error("empty selection must remain below the member floor for the report layer")
	}
}

func TestBuildArtifactDigestMatchesManifest(t *testing.T) {
	result := buildFixture(t)
	if got := sha256Hex(result.Artifact); got != *result.Manifest.Registry.ArtifactSHA256 {
		t.Errorf("artifact digest = %s, manifest = %s", got, *result.Manifest.Registry.ArtifactSHA256)
	}
	if got := sha256Hex(readTestdata(t, "registry.json")); got != *result.Manifest.Registry.RawSHA256 {
		t.Errorf("raw digest = %s, manifest = %s", got, *result.Manifest.Registry.RawSHA256)
	}
	if *result.Manifest.Registry.Artifact != ArtifactName {
		t.Errorf("artifact name = %s, want %s", *result.Manifest.Registry.Artifact, ArtifactName)
	}
}
