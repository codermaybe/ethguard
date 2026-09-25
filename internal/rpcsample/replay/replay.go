// Package replay loads recorded JSON-RPC samples for offline playback.
// It checks each record's sha256 and returns the stored response body.
// It does not adjudicate, normalize, or generate fixtures.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ethguard/internal/rpcsample"
)

// LoadBatch reads dir/index.json and every record it names. The sha256 is
// taken over the file bytes, which is what the collector wrote, not a
// re-encoding of the record.
func LoadBatch(dir string) (rpcsample.Index, []rpcsample.Record, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return rpcsample.Index{}, nil, err
	}
	indexRaw, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return rpcsample.Index{}, nil, err
	}
	var index rpcsample.Index
	if err := json.Unmarshal(indexRaw, &index); err != nil {
		return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: decode index: %w", err)
	}
	if index.Schema != rpcsample.IndexSchema || len(index.Records) == 0 {
		return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: %s has no usable index", dir)
	}
	records := make([]rpcsample.Record, 0, len(index.Records))
	for _, entry := range index.Records {
		if entry.File == "" || entry.SHA256 == "" {
			return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: index entry %s/%s is missing file or sha256", entry.RequestID, entry.SourceID)
		}
		path := filepath.Join(root, filepath.FromSlash(entry.File))
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: %s escapes the batch", entry.File)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return rpcsample.Index{}, nil, err
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != entry.SHA256 {
			return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: %s sha256 = %s, index has %s", entry.File, got, entry.SHA256)
		}
		var record rpcsample.Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: decode %s: %w", entry.File, err)
		}
		if record.Schema != rpcsample.RecordSchema {
			return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: %s schema = %s", entry.File, record.Schema)
		}
		if record.RequestID != entry.RequestID || record.Source.ID != entry.SourceID || record.Method != entry.Method {
			return rpcsample.Index{}, nil, fmt.Errorf("rpcsample replay: %s identity does not match the index", entry.File)
		}
		records = append(records, record)
	}
	return index, records, nil
}

// BodyOf returns the recorded JSON-RPC response body. isError is true when
// the body carries a non-null error object. The raw bytes are the stored
// body, not a rewritten envelope.
func BodyOf(record rpcsample.Record) (isError bool, raw json.RawMessage, err error) {
	if strings.TrimSpace(record.Response.Body) == "" {
		return false, nil, fmt.Errorf("rpcsample replay: %s/%s has no response body", record.RequestID, record.Source.ID)
	}
	raw = json.RawMessage(record.Response.Body)
	if !json.Valid(raw) {
		return false, nil, fmt.Errorf("rpcsample replay: %s/%s response body is not JSON", record.RequestID, record.Source.ID)
	}
	var doc struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, nil, fmt.Errorf("rpcsample replay: %s/%s: %w", record.RequestID, record.Source.ID, err)
	}
	if len(doc.Error) > 0 && string(doc.Error) != "null" {
		return true, raw, nil
	}
	return false, raw, nil
}
