package subscription

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common"
)

// This measures the content owner, complete proof, canonical quorum and typed
// delivery together with its Head input. It excludes networking and fixture
// construction; requests and response bytes expose the extra Q cost.
func BenchmarkReceiptsContentRoundTrip(b *testing.B) {
	for _, tc := range []struct {
		name     string
		members  int
		excluded bool
	}{{"forwarded", 1, false}, {"fixed-Q2", 2, false}, {"zero-match-Q2", 2, true}} {
		b.Run(tc.name, func(b *testing.B) {
			base := makeLogsBlock(b, nil, 99, 1)
			a := makeLogsBlock(b, base.head, 100, 1, 1, 0)
			fork := makeLogsBlock(b, base.head, 100, 2, 1, 0)
			lookup := receiptLookup(b, base, a, fork)
			var blocks, receipts, bytes atomic.Uint64
			call := func(ctx context.Context, method string, args ...any) rpc.Response {
				response := lookup(ctx, method, args...)
				bytes.Add(uint64(len(response.Answer.Result)))
				switch method {
				case "eth_getBlockByHash":
					blocks.Add(1)
				case "eth_getBlockReceipts":
					receipts.Add(1)
				}
				return response
			}
			sources := make([]Source, tc.members)
			feeds := make([]*newHeadTestFeed, tc.members)
			for i := range sources {
				source, feed := newHeadSource(fmt.Sprint(i))
				sources[i], feeds[i] = &receiptTestSource{source, call}, feed
			}
			var query *ethapi.TransactionReceiptsQuery
			if tc.excluded {
				hashes := []ethapi.Hash{ethapi.Hash(common.HexToHash("0xab").Hex())}
				query = &ethapi.TransactionReceiptsQuery{TransactionHashes: &hashes}
			}
			mode := rpc.CorroborationMajority
			if tc.members == 1 {
				mode = rpc.CorroborationForwarded
			}
			p := testReceiptsPolicy()
			p.MaxConcurrentRequests = 1
			p.RequestTimeout, p.RecoveryTimeout = 0, 0
			s, err := NewReceipts(context.Background(), context.Background(), func() ([]Source, error) { return sources, nil }, query, p, mode)
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
			pushLogsHead(feeds, base.head)
			if _, err := s.Next(context.Background()); err != nil {
				b.Fatal(err)
			}
			s.wg.Wait()
			blocks.Store(0)
			receipts.Store(0)
			bytes.Store(0)
			notifications := []transport.Notification{{Result: testHeader(a.head)}, {Result: testHeader(fork.head)}}
			var retained uint64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, feed := range feeds {
					feed.events <- notifications[i%2]
				}
				event, err := s.Next(context.Background())
				if err != nil || len(event.Path.Appended) != 1 {
					b.Fatalf("delta: %+v %v", event.Path, err)
				}
				s.wg.Wait()
				if used := s.budget.Used(); used > retained {
					retained = used
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(blocks.Load())/float64(b.N), "blocks/op")
			b.ReportMetric(float64(receipts.Load())/float64(b.N), "receipts/op")
			b.ReportMetric(float64(bytes.Load())/float64(b.N), "response-B/op")
			b.ReportMetric(float64(retained), "content-retained-B")
		})
	}
}
