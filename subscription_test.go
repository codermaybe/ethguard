package ethguard

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestSubscriptionFilterArgMatchesOfficialShape(t *testing.T) {
	rangeQuery, err := ethapi.NewFilterQuery(json.RawMessage(`{"address":["0x0100000000000000000000000000000000000000"],"topics":[["0x0200000000000000000000000000000000000000000000000000000000000000"]]}`))
	if err != nil {
		t.Fatalf("filter query: %v", err)
	}
	arg, err := toFilterArg(rangeQuery)
	if err != nil {
		t.Fatalf("filter arg: %v", err)
	}
	if _, ok := arg["fromBlock"]; !ok {
		t.Fatal("missing default fromBlock")
	}
	if _, ok := arg["toBlock"]; !ok {
		t.Fatal("missing default toBlock")
	}
	// The hash branch is an open object, so fromBlock parses there; the
	// method's own mutual exclusion must reject it before any contact.
	hashQuery, err := ethapi.NewFilterQuery(json.RawMessage(`{"blockHash":"0x0100000000000000000000000000000000000000000000000000000000000000","fromBlock":"0x1"}`))
	if err != nil {
		t.Fatalf("hash query: %v", err)
	}
	if _, err := toFilterArg(hashQuery); err == nil {
		t.Fatal("accepted block hash with range")
	}
}
