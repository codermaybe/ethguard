package subscription

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestDefaultRepresentativeWireSizes(t *testing.T) {
	counts := make([]int, 240)
	for i := range counts {
		counts[i] = 3
	}
	fixture := makeLogsBlockPayload(t, nil, 100, 1, true, counts...)
	receipts := logsJSON(t, fixture.receipts)
	logs, err := proveBlockLogs(fixture.head, fixture.txs, receipts)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := proveSelectedReceipts(fixture.head, fixture.txs, receiptsFilter{}.indices(fixture.txs), receipts)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonicalReceiptBytes=%d twentyWinnerRetention=%d", len(canonical), 20*(uint64(len(canonical))*64+4096))
	t.Logf("transactions=%d logs=%d blockBytes=%d receiptsBytes=%d logRetentionBytes=%d", len(fixture.txs), len(logs), len(fixture.block), len(receipts), logsContentBytes(logs))
}

func TestDefaultsRepresentativeContinuousAndTwentyBlockReorg(t *testing.T) {
	counts := make([]int, 240)
	for i := range counts {
		counts[i] = 3
	}
	common := makeLogsBlockPayload(t, nil, 100, 1, true, counts...)
	old := make([]logsBlockFixture, 20)
	fork := make([]logsBlockFixture, 20)
	blocks := []logsBlockFixture{common}
	parent := common.head
	for i := range old {
		old[i] = makeLogsBlockPayload(t, parent, uint64(101+i), 1, true, counts...)
		parent = old[i].head
		blocks = append(blocks, old[i])
	}
	parent = common.head
	for i := range fork {
		fork[i] = makeLogsBlockPayload(t, parent, uint64(101+i), 2, true, counts...)
		parent = fork[i].head
		blocks = append(blocks, fork[i])
	}
	last := makeLogsBlockPayload(t, parent, 121, 2, true, counts...)
	blocks = append(blocks, last)
	lookup := logsFixtureLookup(t, blocks...)
	t.Run("logs", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var gate chan struct{}
			var active, maximum atomic.Int32
			var maxOwned atomic.Int32
			var peakContent, peakHead atomic.Uint64
			var ownerMu sync.Mutex
			var owner *LogsSubscription
			gated := func(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-gate:
				}
				content := method == "eth_getBlockReceipts" || (method == "eth_getBlockByHash" && len(args) > 1 && args[1] == true)
				if content {
					n := active.Add(1)
					defer active.Add(-1)
					for {
						m := maximum.Load()
						if n <= m || maximum.CompareAndSwap(m, n) {
							break
						}
					}
				}
				time.Sleep(time.Millisecond)
				ownerMu.Lock()
				current := owner
				ownerMu.Unlock()
				if current != nil && content {
					current.mu.Lock()
					owned := int32(len(current.active))
					current.mu.Unlock()
					for {
						m := maxOwned.Load()
						if owned <= m || maxOwned.CompareAndSwap(m, owned) {
							break
						}
					}
					recordDefaultPeak(&peakContent, current.budget.Used())
					recordDefaultPeak(&peakHead, current.head.budget.Used())
				}
				return lookup(ctx, method, args...)
			}
			p := DefaultLogsSubscriptionPolicy()
			s, feeds := startTestLogs(t, context.Background(), `{}`, p, gated)
			ownerMu.Lock()
			owner = s
			ownerMu.Unlock()
			push := func(head *types.Header) {
				synctest.Wait()
				gate = make(chan struct{})
				pushLogsHead(feeds, head)
				synctest.Wait()
				close(gate)
			}
			next := func() LogsEvent {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				event, err := s.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return event
			}
			check := func(event LogsEvent, appended, retracted int) {
				if len(event.Path.Appended) != appended || len(event.Path.Retracted) != retracted {
					t.Fatalf("delta appended=%d retracted=%d want=%d/%d", len(event.Path.Appended), len(event.Path.Retracted), appended, retracted)
				}
				for _, d := range event.Path.Appended {
					if len(d.Logs) != 720 {
						t.Fatalf("logs=%d", len(d.Logs))
					}
				}
			}
			push(common.head)
			check(next(), 1, 0)
			for _, block := range old {
				push(block.head)
				check(next(), 1, 0)
			}
			push(fork[19].head)
			check(next(), 20, 20)
			push(last.head)
			check(next(), 1, 0)
			t.Logf("actual content calls=%d ownedRequests=%d sampledPeakContent=%d sampledPeakHead=%d", maximum.Load(), maxOwned.Load(), peakContent.Load(), peakHead.Load())
			if peakContent.Load() < 2*(p.MaxResponseBytes*256+16384) {
				t.Fatal("two content workspaces were not simultaneously held")
			}
			if maxOwned.Load() < 2 {
				t.Fatal("content request owner never admitted two requests")
			}
			if maximum.Load() < 2 {
				t.Fatalf("concurrency was not exercised: %d", maximum.Load())
			}
			_ = s.Close()
			if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
				t.Fatalf("close budget content=%d head=%d", s.budget.Used(), s.head.budget.Used())
			}
		})
	})
	t.Run("receipts", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var gate chan struct{}
			var active, maximum atomic.Int32
			var maxOwned atomic.Int32
			var peakContent, peakHead atomic.Uint64
			var ownerMu sync.Mutex
			var owner *ReceiptsSubscription
			gated := func(ctx context.Context, method string, args ...any) rpc.Response {
				select {
				case <-ctx.Done():
					return rpc.Response{Exchange: rpc.Exchange{Cause: ctx.Err()}}
				case <-gate:
				}
				content := method == "eth_getBlockReceipts" || (method == "eth_getBlockByHash" && len(args) > 1 && args[1] == true)
				if content {
					n := active.Add(1)
					defer active.Add(-1)
					for {
						m := maximum.Load()
						if n <= m || maximum.CompareAndSwap(m, n) {
							break
						}
					}
				}
				time.Sleep(time.Millisecond)
				ownerMu.Lock()
				current := owner
				ownerMu.Unlock()
				if current != nil && content {
					current.mu.Lock()
					owned := int32(len(current.active))
					current.mu.Unlock()
					for {
						m := maxOwned.Load()
						if owned <= m || maxOwned.CompareAndSwap(m, owned) {
							break
						}
					}
					recordDefaultPeak(&peakContent, current.budget.Used())
					recordDefaultPeak(&peakHead, current.head.budget.Used())
				}
				raw, err := lookup(ctx, method, args...)
				if err != nil {
					return rpc.Response{Exchange: rpc.Exchange{Cause: err}}
				}
				return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}
			}
			p := DefaultReceiptsSubscriptionPolicy()
			s, feeds := startTestReceipts(t, context.Background(), nil, p, gated, gated, gated)
			ownerMu.Lock()
			owner = s
			ownerMu.Unlock()
			push := func(head *types.Header) {
				synctest.Wait()
				gate = make(chan struct{})
				pushLogsHead(feeds, head)
				synctest.Wait()
				close(gate)
			}
			next := func() ReceiptsEvent {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				event, err := s.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return event
			}
			check := func(event ReceiptsEvent, appended, retracted int) {
				if len(event.Path.Appended) != appended || len(event.Path.Retracted) != retracted {
					t.Fatalf("delta appended=%d retracted=%d want=%d/%d", len(event.Path.Appended), len(event.Path.Retracted), appended, retracted)
				}
				for _, d := range event.Path.Appended {
					if len(d.Receipts) != 240 {
						t.Fatalf("receipts=%d", len(d.Receipts))
					}
					for _, r := range d.Receipts {
						if len(r.Logs) != 3 {
							t.Fatalf("receipt logs=%d", len(r.Logs))
						}
					}
				}
			}
			push(common.head)
			check(next(), 1, 0)
			for _, block := range old {
				push(block.head)
				check(next(), 1, 0)
			}
			push(fork[19].head)
			check(next(), 20, 20)
			push(last.head)
			check(next(), 1, 0)
			t.Logf("actual content calls=%d ownedRequests=%d sampledPeakContent=%d sampledPeakHead=%d", maximum.Load(), maxOwned.Load(), peakContent.Load(), peakHead.Load())
			if peakContent.Load() < 2*(p.MaxResponseBytes*256+16384) {
				t.Fatal("two content workspaces were not simultaneously held")
			}
			if maxOwned.Load() < 2 {
				t.Fatal("content request owner never admitted two requests")
			}
			if maximum.Load() < 2 {
				t.Fatalf("concurrency was not exercised: %d", maximum.Load())
			}
			_ = s.Close()
			if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
				t.Fatalf("close budget content=%d head=%d", s.budget.Used(), s.head.budget.Used())
			}
		})
	})
}

func recordDefaultPeak(peak *atomic.Uint64, n uint64) {
	for {
		old := peak.Load()
		if n <= old || peak.CompareAndSwap(old, n) {
			return
		}
	}
}
