package replay

import (
	"os"
	"path/filepath"
	"testing"
)

func batchDir(feature string) string {
	return filepath.Join("..", "testdata", feature)
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
