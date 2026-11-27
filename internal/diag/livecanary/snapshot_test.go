package livecanary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSnapshot(t *testing.T, dir string, raw []byte, mutate func(*Snapshot)) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "raw.json"), raw, 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	snapshot := testSnapshot(raw)
	if mutate != nil {
		mutate(&snapshot)
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

func TestLoadSnapshot(t *testing.T) {
	dir := t.TempDir()
	raw := readTestdata(t, "registry.json")
	path := writeSnapshot(t, dir, raw, nil)
	loaded, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if loaded.URL != ChainlistURL {
		t.Errorf("url = %s", loaded.URL)
	}
	if string(loaded.Raw) != string(raw) {
		t.Error("raw bytes changed")
	}
}

func TestLoadSnapshotRejectsInvalidProvenance(t *testing.T) {
	raw := readTestdata(t, "registry.json")
	tests := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"schema", func(s *Snapshot) { s.SchemaVersion = "ethguard.registry-snapshot/v0" }},
		{"url", func(s *Snapshot) { s.URL = "http://chainlist.org/rpcs.json" }},
		{"non-chainlist url", func(s *Snapshot) { s.URL = "https://attacker.example/registry.json" }},
		{"zero fetched_at", func(s *Snapshot) { s.FetchedAt = "0001-01-01T00:00:00Z" }},
		{"fetched_at", func(s *Snapshot) { s.FetchedAt = "yesterday" }},
		{"raw_file traversal", func(s *Snapshot) { s.RawFile = "../raw.json" }},
		{"raw hash", func(s *Snapshot) { s.RawSHA256 = sha256Hex([]byte("other")) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSnapshot(t, dir, raw, test.mutate)
			if _, err := LoadSnapshot(path); err == nil {
				t.Fatal("LoadSnapshot accepted invalid provenance")
			}
		})
	}
}

func TestLoadSnapshotStrictDecode(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`[]`)
	path := writeSnapshot(t, dir, raw, nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode descriptor: %v", err)
	}
	document["human_note"] = "filled by hand"
	withUnknown, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	path = filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(path, withUnknown, 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("LoadSnapshot accepted an unknown field")
	}
}

func TestLoadSnapshotMissingRawFile(t *testing.T) {
	dir := t.TempDir()
	path := writeSnapshot(t, dir, []byte(`[]`), nil)
	if err := os.Remove(filepath.Join(dir, "raw.json")); err != nil {
		t.Fatalf("remove raw: %v", err)
	}
	_, err := LoadSnapshot(path)
	if err == nil || !strings.Contains(err.Error(), "open raw registry") {
		t.Fatalf("LoadSnapshot error = %v", err)
	}
}
