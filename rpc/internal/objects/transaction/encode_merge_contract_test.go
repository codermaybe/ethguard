package transaction

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// recordedMainnetTransaction loads the recorded mainnet 1559 transaction
// used as the canonical baseline for both merge contracts.
func recordedMainnetTransaction(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/tx-one-mainnet-1559.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["type"]) != `"0x2"` {
		t.Fatalf("fixture is not a 1559 transaction: %s", fields["type"])
	}
	return fields
}

// TestEncodePreservesOfficiallyToleratedMembers pins the open-branch
// contract: the official transaction branches declare no
// additionalProperties:false, so a member outside the declared set is an
// officially tolerated extra. It must reach the canonical value through the
// pruned-key backfill instead of being silently dropped.
func TestEncodePreservesOfficiallyToleratedMembers(t *testing.T) {
	fields := recordedMainnetTransaction(t)
	locked, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(locked)
	if err != nil {
		t.Fatalf("recorded transaction stopped encoding: %v", err)
	}
	fields["maxFeePerBlobGas"] = json.RawMessage(`"0x0"`)
	tolerated, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := Encode(tolerated)
	if err != nil {
		t.Fatalf("officially tolerated member rejected: %v", err)
	}
	var extra struct {
		MaxFeePerBlobGas json.RawMessage `json:"maxFeePerBlobGas"`
	}
	if err := json.Unmarshal(enriched, &extra); err != nil {
		t.Fatal(err)
	}
	if string(extra.MaxFeePerBlobGas) != `"0x0"` {
		t.Fatalf("tolerated member lost from canonical value: %s", enriched)
	}
	if _, err := Encode(encoded); err != nil {
		t.Fatalf("re-encoding canonical value failed: %v", err)
	}
}

// TestEncodeMasksClosedObjectForeignMembers pins the closed-sub-object
// contract: the official accessList item is a closed object
// (additionalProperties:false), so an undeclared member is masked out of the
// comparison key and the canonical value rather than silently merged into
// either. The transaction keeps encoding: the item's official members
// already validated through the typed branch.
func TestEncodeMasksClosedObjectForeignMembers(t *testing.T) {
	fields := recordedMainnetTransaction(t)
	fields["accessList"] = json.RawMessage(`[{"address":"0x0000000000000000000000000000000000000001","storageKeys":[],"vendor":"0x1"}]`)
	annotated, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(annotated)
	if err != nil {
		t.Fatalf("annotated transaction stopped encoding: %v", err)
	}
	for _, payload := range []json.RawMessage{annotated, encoded} {
		key, err := wire.PruneMethod("eth_getTransactionByHash", payload)
		if err != nil {
			t.Fatal(err)
		}
		if accessListHasMember(t, key, "vendor") {
			t.Fatalf("closed-object foreign member reached the comparison key: %s", key)
		}
	}
	var encodedFields struct {
		AccessList []map[string]json.RawMessage `json:"accessList"`
	}
	if err := json.Unmarshal(encoded, &encodedFields); err != nil {
		t.Fatal(err)
	}
	if len(encodedFields.AccessList) != 1 {
		t.Fatalf("access list lost items: %s", encoded)
	}
	if _, exists := encodedFields.AccessList[0]["vendor"]; exists {
		t.Fatalf("closed-object foreign member reached the canonical value: %s", encoded)
	}
}

func accessListHasMember(t *testing.T, key string, member string) bool {
	t.Helper()
	var fields struct {
		AccessList []map[string]json.RawMessage `json:"accessList"`
	}
	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		t.Fatal(err)
	}
	for _, item := range fields.AccessList {
		if _, exists := item[member]; exists {
			return true
		}
	}
	return false
}
