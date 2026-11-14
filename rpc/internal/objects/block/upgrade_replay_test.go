package block

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSlotNullOmissionExplainsCapturedReplayValueMigration(t *testing.T) {
	for _, tc := range []struct{ name, old, current string }{
		{"block-empty-hash", "24aa9a3a74c023c2711db4fc176ca472c33622c0578ef4eda91e9fcb5d27d965", "0a65860908f65abc926bc32d4289c763d9fab9893a3cca979323254f7a9ea9ef"},
		{"block-empty-number", "24aa9a3a74c023c2711db4fc176ca472c33622c0578ef4eda91e9fcb5d27d965", "0a65860908f65abc926bc32d4289c763d9fab9893a3cca979323254f7a9ea9ef"},
		{"block-filled-hash", "47923859f962dbdf16bab5d1dee46033a760ca0e5367b9a2a503f7cdd970266e", "2707926ac8920dcb7a80d4c26eb3acc0d90b819a90ff08817208ac36cf121f88"},
		{"block-filled-number", "47923859f962dbdf16bab5d1dee46033a760ca0e5367b9a2a503f7cdd970266e", "2707926ac8920dcb7a80d4c26eb3acc0d90b819a90ff08817208ac36cf121f88"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23", "records", tc.name+"--s1.json"))
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
			if got := fmt.Sprintf("%x", sha256.Sum256(encoded)); got != tc.current {
				t.Fatalf("current canonical fingerprint = %s, want %s", got, tc.current)
			}
			var value map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if _, exists := value["slotNumber"]; exists {
				t.Fatal("pre-slot block unexpectedly has slotNumber")
			}
			value["slotNumber"] = json.RawMessage("null")
			previous, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(previous)); got != tc.old {
				t.Fatalf("adding only slotNumber:null gives %s, want archived %s", got, tc.old)
			}
		})
	}
}
