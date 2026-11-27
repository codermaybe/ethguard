package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	subcore "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common"
)

func TestContentSubscriptionRejectsOverflowingConcurrencyBeforeNetwork(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("public concurrency overflow requires a 64-bit int")
	}
	concurrency := 1
	concurrency <<= 55
	for _, method := range []string{"logs", "receipts"} {
		t.Run(method, func(t *testing.T) {
			var requests atomic.Int32
			contacted := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				select {
				case contacted <- struct{}{}:
				default:
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(server.Close)
			client, err := New(config.Config{Upstreams: []config.Upstream{{ID: "budget-source", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"), Transport: config.TransportWSS}}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			var closeStream func() error
			if method == "logs" {
				policy := testLogsPolicy()
				policy.MaxConcurrentRequests = concurrency
				stream, openErr := client.SubscribeFilterLogs(t.Context(), logsQuery(t, `{}`), policy)
				err = openErr
				if stream != nil {
					closeStream = stream.Close
				}
			} else {
				policy := testReceiptsPolicy()
				policy.MaxConcurrentRequests = concurrency
				stream, openErr := client.SubscribeTransactionReceipts(t.Context(), nil, policy)
				err = openErr
				if stream != nil {
					closeStream = stream.Close
				}
			}
			if closeStream != nil {
				select {
				case <-contacted:
				case <-time.After(time.Second):
				}
				if closeErr := closeStream(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			client.Close()
			if !errors.Is(err, subcore.ErrMemoryBudget) || closeStream != nil || requests.Load() != 0 {
				t.Fatalf("overflowing concurrency: error=%v opened=%v network requests=%d", err, closeStream != nil, requests.Load())
			}
		})
	}
}

func TestContentFixedBudgetRejectsExtraOverflowAfterSetupCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		setup, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		stream, err := newContentSubscription[LogsEvent](t.Context(), setup, nil, testHeadPolicy(), ^uint64(0), 1, ^uint64(0), rpc.CorroborationForwarded)
		cancel()
		want := subcore.ErrMemoryBudget
		if canceled {
			want = context.Canceled
		}
		if stream != nil || !errors.Is(err, want) {
			t.Fatalf("canceled=%v: subscription=%v error=%v, want %v", canceled, stream != nil, err, want)
		}
	}
}

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

func TestFilterArgPreservesNullableFieldsAndExactExtras(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{}`, `{"fromBlock":"0x0","toBlock":"latest"}`},
		{`{"address":null,"topics":null}`, `{"address":null,"fromBlock":"0x0","toBlock":"latest","topics":null}`},
		{`{"address":[],"topics":[]}`, `{"address":[],"fromBlock":"0x0","toBlock":"latest","topics":[]}`},
		{`{"fromBlock":"earliest","toBlock":"earliest","topics":[null,[]]}`, `{"fromBlock":"0x0","toBlock":"0x0","topics":[null,[]]}`},
		{`{"custom":{"n":123456789012345678901234567890,"e":1e+09,"v":null}}`, `{"custom":{"e":1e+09,"n":123456789012345678901234567890,"v":null},"fromBlock":"0x0","toBlock":"latest"}`},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			arg, err := toFilterArg(logsQuery(t, tc.raw))
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
	q := logsQuery(t, `{"address":["0xcccccccccccccccccccccccccccccccccccccccc"],"topics":[["0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]],"fromBlock":"0x1","custom":{"n":123456789012345678901234567890}}`)
	qRange := q.FilterByBlockRange
	(*qRange.Address.Addresses)[0] = ethapi.Address(strings.ToUpper(string((*qRange.Address.Addresses)[0])))
	arg, err := toFilterArg(q)
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

func TestFilterConsumesTheParsedBranch(t *testing.T) {
	hash := "0X" + strings.Repeat("AB", 32)
	q := ethapi.FilterQuery{FilterByBlockRange: &ethapi.FilterQueryRange{
		AdditionalProperties: map[string]json.RawMessage{"blockHash": json.RawMessage(`"` + hash + `"`)},
	}}
	arg, err := toFilterArg(q)
	if err != nil || len(arg) != 1 || arg["blockHash"] != strings.ToLower(hash) {
		t.Fatalf("parsed hash argument = %v, %v", arg, err)
	}
	filter, err := compileLogsFilter(q)
	if err != nil || !filter.includes(AnchorNode{Hash: common.HexToHash(hash)}) || filter.includes(AnchorNode{}) {
		t.Fatalf("parsed hash filter = %+v, %v", filter, err)
	}
	height := ethapi.Quantity("0x0")
	q.FilterByBlockRange.FromBlock = &ethapi.FilterBlock{BlockNumber: &height}
	if _, err := toFilterArg(q); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("parsed hash/range mixture accepted by unary: %v", err)
	}
	if _, err := compileLogsFilter(q); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("parsed hash/range mixture accepted by stream: %v", err)
	}
}
