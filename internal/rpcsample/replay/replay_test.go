package replay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcsample"
)

const (
	sampleBatchID = "2026-09-23"
)

// featureBatchIDs pins each capture feature's authoritative batch id. The
// three historical batches keep sampleBatchID; a new capture batch adds its
// own entry here and never moves sampleBatchID, which would repoint the
// historical batches. This map is the authoritative feature↔batch-id
// correspondence (capture-golden-contract §4.0).
var featureBatchIDs = map[string]string{
	"read-local":    "2026-10-19",
	"read-volatile": "2026-10-21",
	"read-blockref": "2026-10-21",
}

func featureBatchID(feature string) string {
	if id, ok := featureBatchIDs[feature]; ok {
		return id
	}
	return sampleBatchID
}

func batchDir(feature string) string {
	if id, ok := featureBatchIDs[feature]; ok {
		return filepath.Join("..", "testdata", feature, id)
	}
	return filepath.Join("..", "testdata", feature, sampleBatchID)
}

func TestLoadBatchVerifiesRecordSHA256(t *testing.T) {
	for _, name := range []string{"revert-data", "block-receipt-tx", "eip1898-object", "read-local", "read-volatile", "read-blockref"} {
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
				if err != nil && !hasRecordedError(record, "malformed_json") {
					t.Fatalf("%s/%s: %v", record.RequestID, record.Source.ID, err)
				}
			}
		})
	}
}

func hasRecordedError(record rpcsample.Record, kind string) bool {
	for _, item := range record.Errors {
		if item.Kind == kind {
			return true
		}
	}
	return false
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
	for _, feature := range []string{"revert-data", "block-receipt-tx", "eip1898-object", "read-local", "read-volatile", "read-blockref"} {
		t.Run(feature, func(t *testing.T) {
			dir := batchDir(feature)
			if _, records, err := LoadBatch(dir); err != nil || len(records) == 0 {
				t.Fatalf("LoadBatch: %v", err)
			}
			meta, err := LoadBatchMeta(dir)
			if err != nil {
				t.Fatal(err)
			}
			if meta.BatchID != featureBatchID(feature) || meta.Feature != feature {
				t.Fatalf("batch header = %+v", meta)
			}
			if meta.CapturedAt == "" || meta.Network.ChainID != 1 || meta.Network.Fork == "" || meta.Note == "" {
				t.Fatalf("batch context = %+v", meta)
			}
			if meta.BlockHeight != nil || meta.Web3ClientVersion != nil {
				t.Fatalf("batch invented height or client version: %+v", meta)
			}
			// Only block-reference batches carry a reference_context; the
			// other features keep the optional field nil.
			if feature == "read-blockref" {
				if len(meta.ReferenceContext) == 0 {
					t.Fatal("read-blockref batch has no reference_context")
				}
			} else if len(meta.ReferenceContext) != 0 {
				t.Fatalf("batch invented reference context: %+v", meta)
			}
			if _, err = os.Stat(filepath.Join(dir, "expect.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
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
