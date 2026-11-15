package block

import (
	"bytes"
	"encoding/json"
	"github.com/codermaybe/ethguard/ethapi"
	"os"
	"path/filepath"
	"testing"
)

// Captured source bytes remain independent evidence for the expanded value:
// official size and transaction context now survive source-local encoding.
func TestCapturedReplayCompleteOfficialValue(t *testing.T) {
	for _, name := range []string{"block-empty-hash", "block-empty-number", "block-filled-hash", "block-filled-number"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23", "records", name+"--s1.json"))
			if err != nil {
				t.Fatal(err)
			}
			var record struct{ Response struct{ Body string } }
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			var response struct{ Result json.RawMessage }
			if err := json.Unmarshal([]byte(record.Response.Body), &response); err != nil {
				t.Fatal(err)
			}
			encoded, err := Encode(response.Result)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ethapi.NewBlock(encoded); err != nil {
				t.Fatalf("complete official block rejected: %v", err)
			}
			var source, value map[string]json.RawMessage
			_ = json.Unmarshal(response.Result, &source)
			_ = json.Unmarshal(encoded, &value)
			if !bytes.Equal(value["size"], source["size"]) {
				t.Fatalf("size lost: %s", encoded)
			}
			if _, exists := value["slotNumber"]; exists {
				t.Fatal("pre-slot block unexpectedly has slotNumber")
			}
			var sourceTxs, valueTxs []map[string]json.RawMessage
			_ = json.Unmarshal(source["transactions"], &sourceTxs)
			_ = json.Unmarshal(value["transactions"], &valueTxs)
			if len(sourceTxs) != len(valueTxs) {
				t.Fatal("transaction count changed")
			}
			for i := range sourceTxs {
				for _, field := range []string{"hash", "from", "blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
					if !bytes.Equal(sourceTxs[i][field], valueTxs[i][field]) {
						t.Fatalf("transaction %d %s changed", i, field)
					}
				}
			}
		})
	}
}
