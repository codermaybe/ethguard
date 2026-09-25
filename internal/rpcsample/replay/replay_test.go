package replay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	batchSchemaV1 = "ethguard.batch/v1"
	sampleBatchID = "2026-09-23"
)

func batchDir(feature string) string {
	return filepath.Join("..", "testdata", feature, sampleBatchID)
}

func TestLoadBatchVerifiesRecordSHA256(t *testing.T) {
	for _, name := range []string{"revert-data", "block-receipt-tx", "eip1898-object"} {
		t.Run(name, func(t *testing.T) {
			index, records, err := LoadBatch(batchDir(name))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) == 0 || len(records) != len(index.Records) {
				t.Fatalf("records = %d, index = %d", len(records), len(index.Records))
			}
			for _, record := range records {
				_, _, err := BodyOf(record)
				if record.Response.Body == "" {
					if err == nil {
						t.Fatalf("%s/%s empty body was accepted", record.RequestID, record.Source.ID)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s/%s: %v", record.RequestID, record.Source.ID, err)
				}
			}
		})
	}
}

func TestLoadBatchRejectsChangedRecordBytes(t *testing.T) {
	src := batchDir("revert-data")
	dst := t.TempDir()
	copyTree(t, src, dst)
	recordPath := filepath.Join(dst, "records", "revert-hex--s1.json")
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0x01
	if err := os.WriteFile(recordPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadBatch(dst); err == nil {
		t.Fatal("changed record bytes passed sha256")
	}
}

func TestBatchLayoutKeepsIndexAndExpect(t *testing.T) {
	for _, feature := range []string{"revert-data", "block-receipt-tx", "eip1898-object"} {
		t.Run(feature, func(t *testing.T) {
			dir := batchDir(feature)
			if _, records, err := LoadBatch(dir); err != nil || len(records) == 0 {
				t.Fatalf("LoadBatch: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "batch.json"))
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			var meta batchMeta
			if err := dec.Decode(&meta); err != nil {
				t.Fatal(err)
			}
			if meta.SchemaVersion != batchSchemaV1 || meta.BatchID != sampleBatchID || meta.Feature != feature {
				t.Fatalf("batch header = %+v", meta)
			}
			if meta.CapturedAt == "" || meta.Network.ChainID != 1 || meta.Network.Fork == "" || meta.Note == "" {
				t.Fatalf("batch context = %+v", meta)
			}
			if meta.BlockHeight != nil || meta.Web3ClientVersion != nil {
				t.Fatalf("batch invented height or client version: %+v", meta)
			}
			if _, err = os.Stat(filepath.Join(dir, "expect.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type batchMeta struct {
	SchemaVersion     string  `json:"schema_version"`
	BatchID           string  `json:"batch_id"`
	Feature           string  `json:"feature"`
	CapturedAt        string  `json:"captured_at"`
	Network           network `json:"network"`
	BlockHeight       *uint64 `json:"block_height"`
	Web3ClientVersion *string `json:"web3_client_version"`
	Note              string  `json:"note"`
}

type network struct {
	ChainID uint64 `json:"chain_id"`
	Fork    string `json:"fork"`
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
