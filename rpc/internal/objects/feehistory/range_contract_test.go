package feehistory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestValidateRangeKeepsItsIndependentDomainAndOrder(t *testing.T) {
	for _, tt := range []struct {
		name  string
		count int
		value string
		want  string
	}{
		{"request before malformed response", 0, `{"broken":`, "invalid block count"},
		{"does not validate base fee presence", 1, `{"oldestBlock":"0x1","gasUsedRatio":[0.5]}`, ""},
		{"does not validate ratio values", 1, `{"oldestBlock":"0x1","gasUsedRatio":[2]}`, ""},
		{"count before oldest", 2, `{"oldestBlock":"bad","gasUsedRatio":[0.5]}`, "gasUsedRatio length 1 does not match requested block count 2"},
		{"blob length before oldest", 1, `{"oldestBlock":"bad","gasUsedRatio":[0.5],"baseFeePerBlobGas":[]}`, "baseFeePerBlobGas length 0 does not match gasUsedRatio length 1"},
		{"oldest after length", 1, `{"oldestBlock":"bad","gasUsedRatio":[0.5]}`, "oldestBlock:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRange(rpc.Request{Params: []any{tt.count}}, json.RawMessage(tt.value))
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRange = %v; want %q", err, tt.want)
			}
		})
	}
}
