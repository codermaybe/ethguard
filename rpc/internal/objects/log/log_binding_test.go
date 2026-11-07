package log

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// TestValidateRequestBindingAcceptsEarliestAndUpperHexPrefix pins the
// admission-side grammar to the request-side grammar: the plan layer accepts
// "earliest" (the genesis bound, promised as a 0x0 passthrough by the
// moving-tag matrix) and the upper-case 0X prefix, so the response binding
// must accept both spellings too. A bound the request side let through must
// never resurface as a per-source response-malformation.
func TestValidateRequestBindingAcceptsEarliestAndUpperHexPrefix(t *testing.T) {
	cases := []struct {
		name    string
		from    any
		to      any
		row     string
		wantErr bool
	}{
		{"earliest accepts a genesis-range row", "earliest", "0x5", `{"blockNumber":"0x2"}`, false},
		{"earliest folds to height zero", "earliest", nil, `{"blockNumber":"0x0"}`, false},
		{"earliest still enforces the upper bound", "earliest", "0x5", `{"blockNumber":"0x6"}`, true},
		{"upper-case 0X prefix matches the request grammar", "0X2", nil, `{"blockNumber":"0x2"}`, false},
		{"plain quantity unchanged", "0x1", "0x3", `{"blockNumber":"0x2"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter := map[string]any{}
			if tc.from != nil {
				filter["fromBlock"] = tc.from
			}
			if tc.to != nil {
				filter["toBlock"] = tc.to
			}
			req := rpc.Request{Method: "eth_getLogs", Params: []any{filter}}
			err := ValidateRequestBinding(req, []json.RawMessage{json.RawMessage(tc.row)})
			if tc.wantErr && err == nil {
				t.Fatal("expected a range violation")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("legal bound rejected at admission: %v", err)
			}
		})
	}
}
