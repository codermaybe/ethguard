package transaction

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestEncodeKeepsProtectedTransactionShape(t *testing.T) {
	raw := sampleTransaction(t)
	encoded, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"hash", "from", "blockHash", "blockNumber", "transactionIndex"} {
		if !bytes.Contains(encoded, []byte(`"`+field+`"`)) {
			t.Fatalf("protected field %s missing: %s", field, encoded)
		}
	}
	if bytes.Contains(encoded, []byte(`vendorExtension`)) {
		t.Fatalf("vendor field survived: %s", encoded)
	}
}

func sampleTransaction(t *testing.T) json.RawMessage {
	t.Helper()
	entries, err := os.ReadDir("../../../../internal/rpcsample/testdata/block-receipt-tx/2026-09-23/records")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) < len("tx-one") || entry.Name()[:len("tx-one")] != "tx-one" {
			continue
		}
		raw, err := os.ReadFile("../../../../internal/rpcsample/testdata/block-receipt-tx/2026-09-23/records/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Response struct {
				Body string `json:"body"`
			} `json:"response"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(record.Response.Body), &response); err != nil {
			t.Fatal(err)
		}
		return response.Result
	}
	t.Fatal("tx-one sample not found")
	return nil
}
