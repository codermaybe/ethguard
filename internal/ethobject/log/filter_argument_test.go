package log

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestSubscriptionFilterArgMatchesOfficialShape(t *testing.T) {
	rangeQuery, err := ethapi.NewFilterQuery(json.RawMessage(`{"address":["0x0100000000000000000000000000000000000000"],"topics":[["0x0200000000000000000000000000000000000000000000000000000000000000"]]}`))
	if err != nil {
		t.Fatalf("filter query: %v", err)
	}
	arg, err := FilterArgument(rangeQuery)
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
	if _, err := FilterArgument(hashQuery); err == nil {
		t.Fatal("accepted block hash with range")
	}
}

func TestFilterArgPreservesNullableFieldsAndExactExtras(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{}`, `{"fromBlock":"0x0","toBlock":"latest"}`},
		{`{"address":null,"topics":null}`, `{"address":null,"fromBlock":"0x0","toBlock":"latest","topics":null}`},
		{`{"address":[],"topics":[]}`, `{"address":[],"fromBlock":"0x0","toBlock":"latest","topics":[]}`},
		{`{"fromBlock":"earliest","toBlock":"earliest","topics":[null,[]]}`, `{"fromBlock":"0x0","toBlock":"0x0","topics":[null,[]]}`},
		{`{"custom":{"n":123456789012345678901234567890,"e":1e+09,"v":null}}`, `{"custom":{"e":1e+09,"n":123456789012345678901234567890,"v":null},"fromBlock":"0x0","toBlock":"latest"}`},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			arg, err := FilterArgument(filterQuery(t, tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(arg)
			if err != nil || string(got) != tc.want {
				t.Fatalf("filter argument = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestFilterArgOwnsCanonicalQuery(t *testing.T) {
	q := filterQuery(t, `{"address":["0xcccccccccccccccccccccccccccccccccccccccc"],"topics":[["0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]],"fromBlock":"0x1","custom":{"n":123456789012345678901234567890}}`)
	qRange := q.FilterByBlockRange
	(*qRange.Address.Addresses)[0] = ethapi.Address(strings.ToUpper(string((*qRange.Address.Addresses)[0])))
	arg, err := FilterArgument(q)
	if err != nil {
		t.Fatal(err)
	}
	(*qRange.Address.Addresses)[0] = ethapi.Address("0x" + strings.Repeat("0", 40))
	(*(**qRange.Topics)[0].MultipleTopicMatch)[0] = ethapi.Hash("0x" + strings.Repeat("0", 64))
	*qRange.FromBlock.BlockNumber = "0xff"
	copy(qRange.AdditionalProperties["custom"], []byte(`{"n":0}`))
	got, err := json.Marshal(arg)
	want := `{"address":["0xcccccccccccccccccccccccccccccccccccccccc"],"custom":{"n":123456789012345678901234567890},"fromBlock":"0x1","toBlock":"latest","topics":[["0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]]}`
	if err != nil || string(got) != want {
		t.Fatalf("filter argument after caller mutation = %s, %v; want %s", got, err, want)
	}
}

func filterQuery(t *testing.T, raw string) ethapi.FilterQuery {
	t.Helper()
	query, err := ethapi.NewFilterQuery(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return query
}
