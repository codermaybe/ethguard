package block

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

func TestFullBlockOwnsCanonicalBytesAndDeliversTheirTransactionView(t *testing.T) {
	raw, req := completeBlock(t)
	canonical, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Transactions types.Transactions `json:"transactions"`
	}
	if err := json.Unmarshal(canonical, &want); err != nil {
		t.Fatal(err)
	}
	full, err := AdmitBlock(t.Context(), req, raw, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	for attempt := 0; attempt < 2; attempt++ {
		got, err := full.Transactions()
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, err := json.Marshal(want.Transactions)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatal("delivery changed the canonical transaction representation")
		}
		got[0] = nil
	}
}
