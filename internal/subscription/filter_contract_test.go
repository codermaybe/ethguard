package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard/ethapi"
	objectlog "github.com/codermaybe/ethguard/internal/ethobject/log"
	"github.com/codermaybe/ethguard/rpc"
)

func TestContentFixedBudgetRejectsExtraOverflowAfterSetupCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		setup, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		stream, err := newContentSubscription[LogsEvent](t.Context(), setup, nil, testHeadPolicy(), ^uint64(0), 1, ^uint64(0), rpc.CorroborationForwarded)
		cancel()
		want := ErrMemoryBudget
		if canceled {
			want = context.Canceled
		}
		if stream != nil || !errors.Is(err, want) {
			t.Fatalf("canceled=%v: subscription=%v error=%v, want %v", canceled, stream != nil, err, want)
		}
	}
}

func TestFilterConsumesTheParsedBranch(t *testing.T) {
	hash := "0X" + strings.Repeat("AB", 32)
	q := ethapi.FilterQuery{FilterByBlockRange: &ethapi.FilterQueryRange{
		AdditionalProperties: map[string]json.RawMessage{"blockHash": json.RawMessage(`"` + hash + `"`)},
	}}
	arg, err := objectlog.FilterArgument(q)
	if err != nil || len(arg) != 1 || arg["blockHash"] != strings.ToLower(hash) {
		t.Fatalf("parsed hash argument = %v, %v", arg, err)
	}
	filter, err := compileLogsFilter(q)
	if err != nil || !filter.includes(AnchorNode{Hash: common.HexToHash(hash)}) || filter.includes(AnchorNode{}) {
		t.Fatalf("parsed hash filter = %+v, %v", filter, err)
	}
	height := ethapi.Quantity("0x0")
	q.FilterByBlockRange.FromBlock = &ethapi.FilterBlock{BlockNumber: &height}
	if _, err := objectlog.FilterArgument(q); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("parsed hash/range mixture accepted by unary: %v", err)
	}
	if _, err := compileLogsFilter(q); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("parsed hash/range mixture accepted by stream: %v", err)
	}
}
