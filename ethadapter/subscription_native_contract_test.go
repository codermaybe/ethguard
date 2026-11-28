package ethadapter_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum/common"
)

func TestIndependentNativeHeadTerminalBypassesUnreadFIFOAndKeepsCause(t *testing.T) {
	base := bridgeFixture(t, common.Hash{}, 17, 1, 0, 0)
	second := bridgeFixture(t, base.header.Hash(), 18, 2, 0, 0)
	middle := bridgeFixture(t, second.header.Hash(), 19, 3, 0, 0)
	tip := bridgeFixture(t, middle.header.Hash(), 20, 4, 0, 0)
	peer := newBridgePeer(t, base, second, middle, tip)
	client := bridgeClient(t, peer)
	policy := ethguard.DefaultHeadSubscriptionPolicy()
	policy.RetryDelay = 0 // This test selects terminal disconnect rather than reconnect.
	sub, err := client.Core().SubscribeNewHead(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	peer.push(t, base)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	event, err := sub.Next(ctx)
	if err != nil || len(event.Path.Appended) != 1 {
		t.Fatalf("native birth=%+v %v", event, err)
	}
	peer.push(t, second)
	peer.push(t, tip)
	peer.wait(t, func(requests []bridgeRequest, _ int) bool {
		for _, r := range requests {
			if r.Method == "eth_getBlockByHash" && len(r.Params) > 0 && strings.Contains(string(r.Params[0]), middle.header.Hash().Hex()) {
				return true
			}
		}
		return false
	})
	peer.stop()
	select {
	case <-sub.Terminated():
	case <-ctx.Done():
		t.Fatal("producer terminal waited for unread Next event")
	}
	cause := sub.TerminalError()
	if cause == nil {
		t.Fatal("source exhaustion was reported as intentional close")
	}
	event, err = sub.Next(ctx)
	if err != nil || len(event.Path.Appended) == 0 || event.Path.Appended[0].Node.Hash != second.header.Hash() {
		t.Fatalf("terminal bypass discarded/reordered existing FIFO event: %+v %v", event, err)
	}
	for attempts := 0; attempts < 4; attempts++ {
		_, err = sub.Next(ctx)
		if err != nil {
			break
		}
	}
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		t.Fatalf("FIFO lost failure terminal: %v", err)
	}
	if !errors.Is(sub.TerminalError(), cause) {
		t.Fatal("Next consumption changed producer terminal cause")
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(sub.TerminalError(), cause) {
		t.Fatal("Close replaced an already-owned failure")
	}
}

func TestIndependentNativeContentTerminalBypassesUnreadFIFOAndKeepsCause(t *testing.T) {
	for _, kind := range []string{"logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			base := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
			second := bridgeFixture(t, base.header.Hash(), 18, 2, 1, 1)
			third := bridgeFixture(t, second.header.Hash(), 19, 3, 1, 1)
			peer := newBridgePeer(t, base, second, third)
			client := bridgeClient(t, peer)
			var terminal interface {
				Terminated() <-chan struct{}
				TerminalError() error
				Close() error
			}
			var next func(context.Context) (common.Hash, error)
			if kind == "logs" {
				q, err := ethapi.NewFilterQuery([]byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				policy := ethguard.DefaultLogsSubscriptionPolicy()
				policy.Head.RetryDelay = 0 // Select terminal failure, not reconnect.
				policy.RetryDelay = 0
				s, err := client.Core().SubscribeFilterLogs(t.Context(), q, policy)
				if err != nil {
					t.Fatal(err)
				}
				terminal = s
				next = func(ctx context.Context) (common.Hash, error) {
					e, err := s.Next(ctx)
					if err != nil {
						return common.Hash{}, err
					}
					if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Logs) != 1 {
						return common.Hash{}, errors.New("unexpected logs FIFO event")
					}
					return e.Path.Appended[0].Node.Hash, nil
				}
			} else {
				policy := ethguard.DefaultReceiptsSubscriptionPolicy()
				policy.Head.RetryDelay = 0
				policy.RetryDelay = 0
				s, err := client.Core().SubscribeTransactionReceipts(t.Context(), nil, policy)
				if err != nil {
					t.Fatal(err)
				}
				terminal = s
				next = func(ctx context.Context) (common.Hash, error) {
					e, err := s.Next(ctx)
					if err != nil {
						return common.Hash{}, err
					}
					if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Receipts) != 1 {
						return common.Hash{}, errors.New("unexpected receipts FIFO event")
					}
					return e.Path.Appended[0].Node.Hash, nil
				}
			}
			defer func() { _ = terminal.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			peer.push(t, base)
			if got, err := next(ctx); err != nil || got != base.header.Hash() {
				t.Fatalf("native content birth=%s %v", got, err)
			}
			peer.push(t, second)
			peer.push(t, third)
			peer.wait(t, func(requests []bridgeRequest, _ int) bool {
				for _, r := range requests {
					if r.Method == "eth_getBlockReceipts" && len(r.Params) == 1 && strings.Contains(string(r.Params[0]), third.header.Hash().Hex()) {
						return true
					}
				}
				return false
			})
			peer.stop()
			select {
			case <-terminal.Terminated():
			case <-ctx.Done():
				t.Fatal("content terminal waited for unread FIFO")
			}
			cause := terminal.TerminalError()
			if cause == nil {
				t.Fatal("content producer failure lost stable cause")
			}
			if got, err := next(ctx); err != nil || got != second.header.Hash() {
				t.Fatalf("content terminal discarded earlier queued event: %s %v", got, err)
			}
			var err error
			for range 4 {
				_, err = next(ctx)
				if err != nil {
					break
				}
			}
			if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
				t.Fatalf("content FIFO lost failure terminal: %v", err)
			}
			if !errors.Is(terminal.TerminalError(), cause) {
				t.Fatal("content Next replaced stable terminal cause")
			}
			if err := terminal.Close(); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(terminal.TerminalError(), cause) {
				t.Fatal("content Close replaced failure cause")
			}
		})
	}
}

type bridgeBudget struct {
	mu           sync.Mutex
	used         uint64
	reject       bool
	rejected     chan struct{}
	rejectedOnce sync.Once
}

func (b *bridgeBudget) Reserve(size uint64) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reject {
		b.rejectedOnce.Do(func() { close(b.rejected) })
		return nil, ethguard.ErrHeadMemoryBudget
	}
	b.used += size
	return sync.OnceFunc(func() { b.mu.Lock(); b.used -= size; b.mu.Unlock() }), nil
}
func (b *bridgeBudget) Used() uint64 { b.mu.Lock(); defer b.mu.Unlock(); return b.used }

type bridgeNativeStream struct {
	next     func(context.Context) (common.Hash, error)
	transfer func(context.Context, ethguard.SubscriptionMemoryBudget) (common.Hash, func(), error)
	close    func() error
}

func bridgeNative(t *testing.T, kind string, client *ethguard.Client) bridgeNativeStream {
	t.Helper()
	switch kind {
	case "heads":
		s, err := client.SubscribeNewHead(t.Context(), ethguard.DefaultHeadSubscriptionPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return bridgeNativeStream{close: s.Close, next: func(ctx context.Context) (common.Hash, error) {
			e, err := s.Next(ctx)
			if err != nil {
				return common.Hash{}, err
			}
			if len(e.Path.Appended) != 1 {
				return common.Hash{}, errors.New("wrong head event shape")
			}
			return e.Path.Appended[0].Node.Hash, nil
		}, transfer: func(ctx context.Context, b ethguard.SubscriptionMemoryBudget) (common.Hash, func(), error) {
			e, hold, err := s.NextWithBudget(ctx, b)
			if err != nil {
				return common.Hash{}, hold, err
			}
			if len(e.Path.Appended) != 1 {
				return common.Hash{}, hold, errors.New("wrong transferred head shape")
			}
			return e.Path.Appended[0].Node.Hash, hold, nil
		}}
	case "logs":
		q, err := ethapi.NewFilterQuery([]byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		s, err := client.SubscribeFilterLogs(t.Context(), q, ethguard.DefaultLogsSubscriptionPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return bridgeNativeStream{close: s.Close, next: func(ctx context.Context) (common.Hash, error) {
			e, err := s.Next(ctx)
			if err != nil {
				return common.Hash{}, err
			}
			if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Logs) != 1 {
				return common.Hash{}, errors.New("wrong logs event shape")
			}
			return e.Path.Appended[0].Node.Hash, nil
		}, transfer: func(ctx context.Context, b ethguard.SubscriptionMemoryBudget) (common.Hash, func(), error) {
			e, hold, err := s.NextWithBudget(ctx, b)
			if err != nil {
				return common.Hash{}, hold, err
			}
			if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Logs) != 1 {
				return common.Hash{}, hold, errors.New("wrong transferred logs shape")
			}
			return e.Path.Appended[0].Node.Hash, hold, nil
		}}
	case "receipts":
		s, err := client.SubscribeTransactionReceipts(t.Context(), nil, ethguard.DefaultReceiptsSubscriptionPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return bridgeNativeStream{close: s.Close, next: func(ctx context.Context) (common.Hash, error) {
			e, err := s.Next(ctx)
			if err != nil {
				return common.Hash{}, err
			}
			if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Receipts) != 1 {
				return common.Hash{}, errors.New("wrong receipts event shape")
			}
			return e.Path.Appended[0].Node.Hash, nil
		}, transfer: func(ctx context.Context, b ethguard.SubscriptionMemoryBudget) (common.Hash, func(), error) {
			e, hold, err := s.NextWithBudget(ctx, b)
			if err != nil {
				return common.Hash{}, hold, err
			}
			if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Receipts) != 1 {
				return common.Hash{}, hold, errors.New("wrong transferred receipts shape")
			}
			return e.Path.Appended[0].Node.Hash, hold, nil
		}}
	default:
		t.Fatal("unknown native fixture kind")
		return bridgeNativeStream{}
	}
}

func TestIndependentNativeBudgetTransferRejectsWithoutConsumingPayload(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			block := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
			peer := newBridgePeer(t, block)
			client := bridgeClient(t, peer)
			native := bridgeNative(t, kind, client.Core())
			defer func() { _ = native.close() }()
			peer.push(t, block)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			budget := &bridgeBudget{reject: true, rejected: make(chan struct{})}
			got, release, err := native.transfer(ctx, budget)
			if !errors.Is(err, ethguard.ErrHeadMemoryBudget) || got != (common.Hash{}) || budget.Used() != 0 {
				t.Fatalf("failed transfer delivered/charged payload: hash=%s err=%v used=%d", got, err, budget.Used())
			}
			if release != nil {
				release()
				release()
			}
			got, err = native.next(ctx)
			if err != nil || got != block.header.Hash() {
				t.Fatalf("reservation rejection consumed the original event: %s %v", got, err)
			}
		})
	}
}

func TestIndependentNativeBudgetTransferLeaseOutlivesCloseAndReleasesOnce(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			block := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
			peer := newBridgePeer(t, block)
			client := bridgeClient(t, peer)
			native := bridgeNative(t, kind, client.Core())
			defer func() { _ = native.close() }()
			peer.push(t, block)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			budget := &bridgeBudget{}
			got, release, err := native.transfer(ctx, budget)
			if err != nil || release == nil || got != block.header.Hash() || budget.Used() == 0 {
				t.Fatalf("transfer did not charge destination before publishing: %s %v used=%d", got, err, budget.Used())
			}
			defer release()
			held := budget.Used()
			if err := native.close(); err != nil {
				t.Fatal(err)
			}
			if budget.Used() != held {
				t.Fatal("native Close released externally-held event ownership")
			}
			release()
			release()
			if budget.Used() != 0 {
				t.Fatal("external release did not return destination ledger to zero")
			}
		})
	}
}

func TestIndependentNativeIntentionalCloseHasStableNilTerminal(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			for range 20 {
				peer := newBridgePeer(t)
				client := bridgeClient(t, peer)
				var sub interface {
					Close() error
					Terminated() <-chan struct{}
					TerminalError() error
				}
				var err error
				switch kind {
				case "heads":
					sub, err = client.Core().SubscribeNewHead(t.Context(), ethguard.DefaultHeadSubscriptionPolicy())
				case "logs":
					q, queryErr := ethapi.NewFilterQuery([]byte(`{}`))
					if queryErr != nil {
						t.Fatal(queryErr)
					}
					sub, err = client.Core().SubscribeFilterLogs(t.Context(), q, ethguard.DefaultLogsSubscriptionPolicy())
				case "receipts":
					sub, err = client.Core().SubscribeTransactionReceipts(t.Context(), nil, ethguard.DefaultReceiptsSubscriptionPolicy())
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := sub.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-sub.Terminated():
				default:
					t.Fatal("Close did not publish terminal signal")
				}
				if err := sub.TerminalError(); err != nil {
					t.Fatalf("active close acquired failure: %v", err)
				}
				if err := sub.Close(); err != nil {
					t.Fatal(err)
				}
				if err := sub.TerminalError(); err != nil {
					t.Fatalf("repeated close changed cause: %v", err)
				}
				client.Close()
				peer.stop()
			}
		})
	}
}
