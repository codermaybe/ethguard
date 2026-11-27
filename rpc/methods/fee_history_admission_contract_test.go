package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethobject/feehistory"
	"github.com/codermaybe/ethguard/rpc"
)

// This is the original admission composition, retained as an independent
// behavior oracle for the shared-model path. Normalize and ValidateRange keep
// their original contracts and remain independently callable.
func admitFeeHistoryViaNormalizedKey(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateFeeHistoryRequest(req); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	if _, err := normalizeFeeHistory(value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	if err := feehistory.ValidateRange(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	if err := validateFeeHistoryRewardWidth(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
}

func TestFeeHistoryAdmissionPreservesNormalizedChain(t *testing.T) {
	type percentiles []float64
	requests := []struct {
		name   string
		params []any
	}{
		{"missing parameters", nil},
		{"invalid count and percentiles", []any{0, "latest", []float64{101}}},
		{"invalid newest block", []any{1, "invalid", []float64{10}}},
		{"invalid percentiles", []any{1, "latest", []float64{50, 10}}},
		{"nan percentile", []any{1, "latest", []float64{math.NaN()}}},
		{"no percentiles", []any{1, "latest", nil}},
		{"empty percentiles", []any{1, "latest", []any{}}},
		{"one percentile", []any{1, "latest", []float64{10}}},
		{"two percentiles", []any{1, "pending", []float64{10, 50}}},
		{"named percentiles", []any{1, "latest", percentiles{10}}},
		{"raw percentiles", []any{"0x1", "0x2", []any{json.Number("10")}}},
		{"two blocks", []any{2, "latest", []float64{10}}},
		{"maximum count", []any{uint64(math.MaxUint64), "latest", []float64{10}}},
		{"maximum count string", []any{"0xffffffffffffffff", "latest", []float64{10}}},
		{"overflow count", []any{new(big.Int).Lsh(big.NewInt(1), 64), "latest", nil}},
		{"leading zero request count", []any{"0x01", "latest", []float64{10}}},
		{"signed request count", []any{"0x+1", "latest", []float64{10}}},
	}
	for _, count := range []any{uint64(1), uint(1), uint32(1), uint16(1), uint8(1), int(1), int64(1), big.NewInt(1), json.Number("1")} {
		requests = append(requests, struct {
			name   string
			params []any
		}{fmt.Sprintf("typed count %T", count), []any{count, "latest", []float64{10}}})
	}
	head := `{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]`
	two := `{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.5,0.25]`
	responses := []struct{ name, value string }{
		{"valid reward", head + `,"reward":[["0x3"]]}`},
		{"absent reward", head + `}`},
		{"null reward", head + `,"reward":null}`},
		{"empty reward", head + `,"reward":[]}`},
		{"empty reward row", head + `,"reward":[[]]}`},
		{"null reward row", head + `,"reward":[null]}`},
		{"two samples", head + `,"reward":[["0x3","0x4"]]}`},
		{"wrong reward row count", head + `,"reward":[["0x3"],["0x4"]]}`},
		{"nonuniform reward", two + `,"reward":[["0x3"],["0x4","0x5"]]}`},
		{"valid two blocks", two + `,"reward":[["0x3"],["0x4"]]}`},
		{"uppercase reward only", head + `,"Reward":[["0x3"]]}`},
		{"later alias has different width", head + `,"reward":[["0x3"]],"Reward":[["0x3","0x4"]]}`},
		{"exact key has different width", head + `,"reward":[["0x3","0x4"]],"Reward":[["0x3"]]}`},
		{"exact reward overwritten by null alias", head + `,"reward":[["0x3"]],"Reward":null}`},
		{"exact null followed by alias", head + `,"reward":null,"Reward":[["0x3"]]}`},
		{"duplicate exact reward last wins", head + `,"reward":[["0x3","0x4"]],"reward":[["0x3"]]}`},
		{"invalid reward type before legal alias", head + `,"reward":5,"Reward":[["0x3"]]}`},
		{"invalid alias before legal reward", head + `,"Reward":5,"reward":[["0x3"]]}`},
		{"gas alias overwrites array", head + `,"GasUsedRatio":[],"reward":[["0x3"]]}`},
		{"oldest alias overwrites quantity", head + `,"OldestBlock":"bad","reward":[["0x3"]]}`},
		{"valid blobs", head + `,"reward":[["0x3"]],"baseFeePerBlobGas":["0x0","0x2"],"blobGasUsedRatio":[0.25]}`},
		{"null blobs", head + `,"reward":[["0x3"]],"baseFeePerBlobGas":null,"blobGasUsedRatio":null}`},
		{"empty blob base", head + `,"reward":[["0x3"]],"baseFeePerBlobGas":[]}`},
		{"empty blob ratios", head + `,"reward":[["0x3"]],"blobGasUsedRatio":[]}`},
		{"bad blob quantity", head + `,"reward":[["0x3"]],"baseFeePerBlobGas":["0x00","0x2"]}`},
		{"bad blob ratio", head + `,"reward":[["0x3"]],"blobGasUsedRatio":[1.1]}`},
		{"vendor member", head + `,"reward":[["0x3"]],"vendor":{"untrusted":true}}`},
		{"missing oldest and wrong base", `{"baseFeePerGas":[],"gasUsedRatio":[0.5]}`},
		{"invalid oldest and wrong base", `{"oldestBlock":"bad","baseFeePerGas":[],"gasUsedRatio":[0.5]}`},
		{"wrong base and reward", `{"oldestBlock":"0x1","baseFeePerGas":[],"gasUsedRatio":[0.5],"reward":[]}`},
		{"invalid base quantity", `{"oldestBlock":"0x1","baseFeePerGas":["0x01","0x2"],"gasUsedRatio":[0.5]}`},
		{"zero supported blocks", `{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[]}`},
		{"null object", `null`},
		{"array response", `[]`},
		{"malformed object", `{"oldestBlock":`},
		{"trailing value", head + `} true`},
	}
	for _, ratio := range []string{`0.50`, `1e0`, `1.1`, `-0.1`, `"0.5"`, `null`, `true`, `0.1234567890123456789`} {
		responses = append(responses, struct{ name, value string }{"ratio " + ratio, `{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[` + ratio + `],"reward":[["0x3"]]}`})
	}
	for _, request := range requests {
		for _, response := range responses {
			t.Run(request.name+"/"+response.name, func(t *testing.T) {
				req := rpc.Request{Method: MethodFeeHistory, Params: request.params}
				input := json.RawMessage(response.value)
				before := bytes.Clone(input)
				want, wantErr := admitFeeHistoryViaNormalizedKey(context.Background(), req, input, nil)
				got, err := admitFeeHistory(context.Background(), req, input, nil)
				if !bytes.Equal(got, want) || (got == nil) != (want == nil) || (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) {
					t.Fatalf("admit = %s, %v; original chain = %s, %v", got, err, want, wantErr)
				}
				for _, sentinel := range []error{rpc.ErrInvalidRequest, rpc.ErrMalformedResponse} {
					if errors.Is(err, sentinel) != errors.Is(wantErr, sentinel) {
						t.Fatalf("sentinel %v changed: %v vs %v", sentinel, err, wantErr)
					}
				}
				if !bytes.Equal(input, before) {
					t.Fatal("admission rewrote response input")
				}
			})
		}
	}
}

func TestFeeHistoryAdmissionKeepsFailurePrecedence(t *testing.T) {
	head := `{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]`
	for _, tt := range []struct {
		name  string
		count any
		value string
		kind  error
		text  string
	}{
		{"request before response", 0, `null`, rpc.ErrInvalidRequest, "blockCount must be positive"},
		{"oldest before count", 2, `{"oldestBlock":"bad","baseFeePerGas":[],"gasUsedRatio":[0.5]}`, rpc.ErrMalformedResponse, "feeHistory: oldestBlock:"},
		{"array relationship before count", 2, `{"oldestBlock":"0x1","baseFeePerGas":[],"gasUsedRatio":[0.5]}`, rpc.ErrMalformedResponse, "baseFeePerGas must have gasUsedRatio length plus one"},
		{"reward quantity before count", 2, head + `,"reward":[["bad"]]}`, rpc.ErrMalformedResponse, "feeHistory: reward[0][0]:"},
		{"count before absent reward", 2, head + `}`, rpc.ErrMalformedResponse, "gasUsedRatio length 1 does not match requested block count 2"},
		{"count before reward width", 2, head + `,"reward":[["0x1","0x2"]]}`, rpc.ErrMalformedResponse, "gasUsedRatio length 1 does not match requested block count 2"},
		{"requested reward width", 1, head + `,"reward":[["0x1","0x2"]]}`, rpc.ErrMalformedResponse, "reward row 0 width 2 does not match the 1 requested percentiles"},
		{"exact reward key differs from struct alias", 1, head + `,"Reward":[["0x1"]]}`, rpc.ErrMalformedResponse, "reward absent but 1 percentiles requested"},
		{"null alias does not erase exact key for width", 1, head + `,"reward":[["0x1"]],"Reward":null}`, nil, ""},
		{"range retains its quantity grammar", "0x+1", head + `,"reward":[["0x1"]]}`, rpc.ErrMalformedResponse, "invalid block count"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := rpc.Request{Method: MethodFeeHistory, Params: []any{tt.count, "latest", []float64{10}}}
			_, err := admitFeeHistory(context.Background(), req, json.RawMessage(tt.value), nil)
			if tt.kind == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, tt.kind) || !bytes.Contains([]byte(err.Error()), []byte(tt.text)) {
				t.Fatalf("admission error = %v; want %v containing %q", err, tt.kind, tt.text)
			}
		})
	}
}
