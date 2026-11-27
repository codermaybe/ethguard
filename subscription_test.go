package ethguard

import (
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
)

func TestContentSubscriptionRejectsOverflowingConcurrencyBeforeNetwork(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("public concurrency overflow requires a 64-bit int")
	}
	headPolicy := HeadSubscriptionPolicy{MemoryBytes: 8 << 20, RetainedNodes: 16, MaxConcurrentRequests: 8, WireMessages: 8, WireBytes: 1 << 16, MaxMessageBytes: 1 << 14}
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
				policy := LogsSubscriptionPolicy{Head: headPolicy, MemoryBytes: 32 << 20, MaxResponseBytes: 1 << 14, MaxConcurrentRequests: 3, RequestTimeout: time.Second, RecoveryTimeout: 2 * time.Second}
				policy.MaxConcurrentRequests = concurrency
				stream, openErr := client.SubscribeFilterLogs(t.Context(), ethapi.FilterQuery{FilterByBlockRange: &ethapi.FilterQueryRange{}}, policy)
				err = openErr
				if stream != nil {
					closeStream = stream.Close
				}
			} else {
				policy := ReceiptsSubscriptionPolicy{Head: headPolicy, MemoryBytes: 64 << 20, MaxResponseBytes: 1 << 14, MaxConcurrentRequests: 3, RequestTimeout: time.Second, RecoveryTimeout: 3 * time.Second}
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
			if !errors.Is(err, ErrHeadMemoryBudget) || closeStream != nil || requests.Load() != 0 {
				t.Fatalf("overflowing concurrency: error=%v opened=%v network requests=%d", err, closeStream != nil, requests.Load())
			}
		})
	}
}
