package livecanary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Snapshot describes one fetched registry document. The raw file is the
// fetcher's evidence; the manifest only publishes a redacted copy of it.
type Snapshot struct {
	SchemaVersion string `json:"schema_version"`
	URL           string `json:"url"`
	FetchedAt     string `json:"fetched_at"`
	RawFile       string `json:"raw_file"`
	RawSHA256     string `json:"raw_sha256"`
}

type LoadedSnapshot struct {
	Snapshot
	Raw []byte
}

func (s Snapshot) Validate() error {
	if s.SchemaVersion != SnapshotSchemaVersion {
		return fmt.Errorf("snapshot.schema_version: got %q, want %q", s.SchemaVersion, SnapshotSchemaVersion)
	}
	if err := validateHTTPSURL("snapshot.url", s.URL); err != nil {
		return err
	}
	if err := validateTimestamp("snapshot.fetched_at", s.FetchedAt); err != nil {
		return err
	}
	if s.RawFile == "" || filepath.IsAbs(s.RawFile) || strings.ContainsAny(s.RawFile, `/\`) ||
		s.RawFile == "." || s.RawFile == ".." {
		return fmt.Errorf("snapshot.raw_file: must be a plain file name next to the descriptor")
	}
	if !sha256RE.MatchString(s.RawSHA256) {
		return fmt.Errorf("snapshot.raw_sha256: not a lowercase SHA-256")
	}
	return nil
}

// LoadSnapshot reads a snapshot descriptor and its raw registry bytes, then
// verifies the declared digest. This is the only place registry bytes enter
// the generator.
func LoadSnapshot(path string) (*LoadedSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var snapshot Snapshot
	if err := dec.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("snapshot: parse: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("snapshot: trailing data")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}

	rawPath := filepath.Join(filepath.Dir(path), snapshot.RawFile)
	file, err := os.Open(rawPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: open raw registry: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, RegistryMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("snapshot: read raw registry: %w", err)
	}
	if len(raw) > RegistryMaxBytes {
		return nil, fmt.Errorf("snapshot: raw registry exceeds %d bytes", RegistryMaxBytes)
	}
	if actual := sha256Hex(raw); actual != snapshot.RawSHA256 {
		return nil, fmt.Errorf("snapshot: raw_sha256 mismatch: got %s, want %s", actual, snapshot.RawSHA256)
	}
	return &LoadedSnapshot{Snapshot: snapshot, Raw: raw}, nil
}
