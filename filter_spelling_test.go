package ethguard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestFilterCompatibilityPreservesOpenRequestContent(t *testing.T) {
	extra := json.RawMessage(`{"text":"0xAB","address":"0XCD","number":9007199254740993,"nested":["0XAB"]}`)
	q := ethapi.FilterQuery{FilterByBlockRange: &ethapi.FilterQueryRange{
		AdditionalProperties: map[string]json.RawMessage{"custom": extra},
	}}
	frozen, err := freezeFilterQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frozen.FilterByBlockRange.AdditionalProperties["custom"], extra) {
		t.Fatalf("unknown request content was rewritten: %s", frozen.FilterByBlockRange.AdditionalProperties["custom"])
	}
	copy(extra, []byte(`{"text":"changed"}`))
	if bytes.Equal(frozen.FilterByBlockRange.AdditionalProperties["custom"], extra) {
		t.Fatal("frozen request aliases caller bytes")
	}
}

func TestFilterCompatibilityLeavesMalformedKnownMembersInvalid(t *testing.T) {
	for _, raw := range []string{
		`{"fromBlock":"0X00"}`, `{"fromBlock":"LATEST"}`,
		`{"blockHash":"0XAB"}`, `{"blockHash":12}`,
		`{"address":[["0X` + strings.Repeat("AB", 20) + `"]]}`,
		`{"topics":[[["0X` + strings.Repeat("AB", 32) + `"]]]}`,
		`{"address":{"value":"0XAB"}}`, `{"topics":[{"value":"0XAB"}]}`,
	} {
		candidate, err := foldFilterSpelling(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ethapi.NewFilterQuery(candidate); err == nil {
			t.Errorf("malformed filter was repaired: %s -> %s", raw, candidate)
		}
	}
}
