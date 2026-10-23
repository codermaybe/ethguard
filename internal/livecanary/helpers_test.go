package livecanary

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testRunID = "01JAZ3K4M5N6P7Q8R9S0T1V2WX"

func testProvenance() Provenance {
	return Provenance{
		Environment:  Environment{Kind: "local", RunnerClass: "local"},
		SourceCommit: strings.Repeat("a", 40),
		GoVersion:    "go1.25.3",
		RunID:        testRunID,
		GeneratedAt:  time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
}

func testSnapshot(raw []byte) Snapshot {
	return Snapshot{
		SchemaVersion: SnapshotSchemaVersion,
		URL:           ChainlistURL,
		FetchedAt:     "2026-10-23T11:59:00Z",
		RawFile:       "raw.json",
		RawSHA256:     sha256Hex(raw),
	}
}

// buildFixture exercises the full generator against the synthetic registry.
func buildFixture(t *testing.T) BuildResult {
	t.Helper()
	raw := readTestdata(t, "registry.json")
	result, err := Build(BuildInput{
		Snapshot:   testSnapshot(raw),
		Raw:        raw,
		Provenance: testProvenance(),
		ChainID:    ChainID,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return result
}

func cloneManifest(t *testing.T, manifest Manifest) Manifest {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest clone: %v", err)
	}
	var clone Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&clone); err != nil {
		t.Fatalf("decode manifest clone: %v", err)
	}
	return clone
}

func stringPointer(value string) *string {
	return &value
}
