package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"

	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

type receiptTestSource struct {
	*newHeadTestSource
	call func(context.Context, string, ...any) rpc.Response
}

func (s *receiptTestSource) Call(ctx context.Context, method string, args ...any) rpc.Response {
	return s.call(ctx, method, args...)
}
func (s *receiptTestSource) CallContext(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	response := s.Call(ctx, method, args...)
	if err := response.Err(); err != nil {
		return nil, err
	}
	return response.Answer.Result, nil
}

func receiptLookup(t testing.TB, blocks ...logsBlockFixture) func(context.Context, string, ...any) rpc.Response {
	t.Helper()
	lookup := logsFixtureLookup(t, blocks...)
	return func(ctx context.Context, method string, args ...any) rpc.Response {
		raw, err := lookup(ctx, method, args...)
		if err != nil {
			return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: err}}
		}
		return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}
	}
}

func testReceiptsPolicy() ReceiptsSubscriptionPolicy {
	return ReceiptsSubscriptionPolicy{Head: testHeadPolicy(), MemoryBytes: 64 << 20, MaxResponseBytes: 1 << 14, MaxConcurrentRequests: 3, RequestTimeout: time.Second, RecoveryTimeout: 3 * time.Second}
}

func startTestReceipts(t *testing.T, owner context.Context, query *ethapi.TransactionReceiptsQuery, p ReceiptsSubscriptionPolicy, calls ...func(context.Context, string, ...any) rpc.Response) (*ReceiptsSubscription, []*newHeadTestFeed) {
	t.Helper()
	sources := make([]Source, len(calls))
	feeds := make([]*newHeadTestFeed, len(calls))
	for i, call := range calls {
		source, feed := newHeadSource(fmt.Sprintf("source-%d", i))
		feeds[i] = feed
		sources[i] = &receiptTestSource{source, call}
	}
	mode := rpc.CorroborationMajority
	if len(calls) == 1 {
		mode = rpc.CorroborationForwarded
	}
	s, err := NewReceipts(owner, context.Background(), func() ([]Source, error) { return sources, nil }, query, p, mode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, feeds
}

func nextReceipts(t *testing.T, s *ReceiptsSubscription) ReceiptsEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	e, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestReceiptsStreamProvesCompleteBirthAndSkippedBlocks(t *testing.T) {
	birth := makeLogsBlock(t, nil, 100, 1, 1, 0)
	middle := makeLogsBlock(t, birth.head, 101, 1)
	end := makeLogsBlock(t, middle.head, 102, 1, 1)
	lookup := receiptLookup(t, birth, middle, end)
	var fullBlocks, receipts atomic.Int32
	call := func(ctx context.Context, method string, args ...any) rpc.Response {
		if method == "eth_getBlockByHash" && args[1] == true {
			fullBlocks.Add(1)
		}
		if method == "eth_getBlockReceipts" {
			receipts.Add(1)
		}
		return lookup(ctx, method, args...)
	}
	p := testReceiptsPolicy()
	p.MaxConcurrentRequests = 1
	s, feeds := startTestReceipts(t, context.Background(), nil, p, call, call)
	pushLogsHead(feeds, birth.head)
	e := nextReceipts(t, s)
	if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Receipts) != 2 || e.Path.NewVersion != 1 {
		t.Fatalf("birth: %+v", e)
	}
	proof := e.Path.Appended[0]
	if proof.ReceiptSupport.Status != rpc.Corroborated || proof.ReceiptSupport.Basis != 2 || proof.ReceiptSupport.Required != 2 || len(proof.ReceiptSupport.Suppliers) != 2 || proof.BlockSupplier.RequestID == 0 {
		t.Fatalf("evidence: %+v", proof)
	}
	pushLogsHead(feeds, end.head)
	e = nextReceipts(t, s)
	if len(e.Path.Appended) != 2 || e.Path.Appended[0].Node.Number != 101 || e.Path.Appended[0].Receipts == nil || len(e.Path.Appended[0].Receipts) != 0 || e.Path.Appended[0].Proof != ReceiptsEmptyBlock || len(e.Path.Appended[0].ReceiptSupport.Suppliers) != 0 || len(e.Path.Appended[1].Receipts) != 1 {
		t.Fatalf("gap: %+v", e)
	}
	if tip, version := s.CommittedTip(); tip.Hash != end.head.Hash() || version != e.Path.NewVersion {
		t.Fatalf("tip: %+v %d", tip, version)
	}
	if fullBlocks.Load() != 3 || receipts.Load() != 4 {
		t.Fatalf("requests: blocks=%d receipts=%d", fullBlocks.Load(), receipts.Load())
	}
}

func TestReceiptsOfficialDeclarationsRequireFixedQuorum(t *testing.T) {
	for _, field := range []string{"from", "gasUsed", "effectiveGasPrice", "blobGasUsed", "blobGasPrice", "contractAddress", "to", "type-presence", "extra-status"} {
		t.Run(field, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			if field == "extra-status" {
				f.receipts[0]["root"] = common.HexToHash("0x1234").Hex()
				r, err := objectreceipt.Decode(logsJSON(t, f.receipts[0]))
				if err != nil {
					t.Fatal(err)
				}
				f.head.ReceiptHash = types.DeriveSha(types.Receipts{r}, trie.NewStackTrie(nil))
				f = rebindReceiptBlock(t, f)
			}
			lookup := receiptLookup(t, f)
			var rows []map[string]any
			if err := json.Unmarshal(logsJSON(t, f.receipts), &rows); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "from", "to", "contractAddress":
				rows[0][field] = common.HexToAddress("0x22").Hex()
			case "type-presence":
				delete(rows[0], "type")
			case "extra-status":
				rows[0]["status"] = "0x0"
			default:
				rows[0][field] = "0x99"
			}
			changed := logsJSON(t, rows)
			other := func(ctx context.Context, method string, args ...any) rpc.Response {
				response := lookup(ctx, method, args...)
				if method == "eth_getBlockReceipts" {
					response.Answer.Result = changed
				}
				return response
			}
			s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup, other)
			pushLogsHead(feeds, f.head)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := s.Next(ctx)
			if !errors.Is(err, rpc.ErrConflict) {
				t.Fatalf("different official field reached quorum: %v", err)
			}
			if _, v := s.CommittedTip(); v != 0 {
				t.Fatal("conflict advanced receipt path")
			}
			// Matching declarations are a Q claim, including fields not in roots.
			matched, matchedFeeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), other, other)
			pushLogsHead(matchedFeeds, f.head)
			e := nextReceipts(t, matched)
			if e.Path.Appended[0].ReceiptSupport.Status != rpc.Corroborated {
				t.Fatal("matching declarations lost Q evidence")
			}
		})
	}
}

func TestReceiptsZeroMatchUsesFrozenTransactionsAndKeepsIdentity(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	lookup := receiptLookup(t, f)
	var receiptCalls atomic.Int32
	call := func(ctx context.Context, method string, args ...any) rpc.Response {
		if method == "eth_getBlockReceipts" {
			receiptCalls.Add(1)
		}
		return lookup(ctx, method, args...)
	}
	hashes := []ethapi.Hash{ethapi.Hash(common.HexToHash("0xabc").Hex())}
	q := &ethapi.TransactionReceiptsQuery{TransactionHashes: &hashes}
	s, feeds := startTestReceipts(t, context.Background(), q, testReceiptsPolicy(), call, call)
	hashes[0] = ethapi.Hash(f.txs[0].Hash().Hex())
	pushLogsHead(feeds, f.head)
	e := nextReceipts(t, s)
	d := e.Path.Appended[0]
	if d.Node.Hash != f.head.Hash() || d.Receipts == nil || len(d.Receipts) != 0 || d.Proof != ReceiptsTransactionsExcluded || len(d.ReceiptSupport.Suppliers) != 0 || receiptCalls.Load() != 0 {
		t.Fatalf("zero proof: %+v calls=%d", d, receiptCalls.Load())
	}
}

func rebindReceiptBlock(t *testing.T, f logsBlockFixture) logsBlockFixture {
	t.Helper()
	hash := f.head.Hash().Hex()
	for _, r := range f.receipts {
		r["blockHash"] = hash
		for _, log := range r["logs"].([]map[string]any) {
			log["blockHash"] = hash
		}
	}
	var block map[string]json.RawMessage
	if err := json.Unmarshal(f.block, &block); err != nil {
		t.Fatal(err)
	}
	var head map[string]json.RawMessage
	if err := json.Unmarshal(testHeader(f.head), &head); err != nil {
		t.Fatal(err)
	}
	for key, value := range head {
		block[key] = value
	}
	var txs []map[string]any
	if err := json.Unmarshal(block["transactions"], &txs); err != nil {
		t.Fatal(err)
	}
	for _, tx := range txs {
		tx["blockHash"] = hash
	}
	block["transactions"] = logsJSON(t, txs)
	f.block = logsJSON(t, block)
	return f
}

func TestReceiptsPresenceIsComparedAndRetained(t *testing.T) {
	for _, shape := range []string{"absent", "null", "value"} {
		t.Run(shape, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			switch shape {
			case "absent":
				delete(f.receipts[0], "contractAddress")
			case "value":
				f.receipts[0]["contractAddress"] = common.HexToAddress("0x123").Hex()
			}
			lookup := receiptLookup(t, f)
			s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup, lookup)
			pushLogsHead(feeds, f.head)
			value := nextReceipts(t, s).Path.Appended[0].Receipts[0]
			switch shape {
			case "absent":
				if value.ContractAddress != nil {
					t.Fatal("absence became explicit")
				}
			case "null":
				if value.ContractAddress == nil || *value.ContractAddress != nil {
					t.Fatal("null changed")
				}
			case "value":
				if value.ContractAddress == nil || *value.ContractAddress == nil || **value.ContractAddress != ethapi.Address(common.HexToAddress("0x123").Hex()) {
					t.Fatal("address value changed")
				}
			}
		})
	}
	f := makeLogsBlock(t, nil, 100, 1, 1)
	lookup := receiptLookup(t, f)
	delete(f.receipts[0], "contractAddress")
	absent := receiptLookup(t, f)
	s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup, absent)
	pushLogsHead(feeds, f.head)
	_, err := s.Next(context.Background())
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("absence and null shared a candidate: %v", err)
	}
}

func TestReceiptsValidateWholeBatchBeforeFiltering(t *testing.T) {
	for _, change := range []string{"missing", "duplicate", "reordered", "root", "location", "timestamp", "removed"} {
		t.Run(change, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1, 1)
			hashes := []ethapi.Hash{ethapi.Hash(f.txs[0].Hash().Hex())}
			switch change {
			case "missing":
				f.receipts = f.receipts[:1]
			case "duplicate":
				f.receipts[1] = f.receipts[0]
			case "reordered":
				f.receipts[0], f.receipts[1] = f.receipts[1], f.receipts[0]
			case "root":
				f.receipts[1]["cumulativeGasUsed"] = "0xa411"
			case "location":
				f.receipts[1]["logs"].([]map[string]any)[0]["logIndex"] = "0x0"
			case "timestamp":
				f.receipts[1]["logs"].([]map[string]any)[0]["blockTimestamp"] = "0x1"
			case "removed":
				f.receipts[1]["logs"].([]map[string]any)[0]["removed"] = true
			}
			lookup := receiptLookup(t, f)
			s, feeds := startTestReceipts(t, context.Background(), &ethapi.TransactionReceiptsQuery{TransactionHashes: &hashes}, testReceiptsPolicy(), lookup, lookup)
			pushLogsHead(feeds, f.head)
			_, err := s.Next(context.Background())
			var terminal *ReceiptsTerminalError
			if !errors.As(err, &terminal) || terminal.PathVersion != 0 {
				t.Fatalf("invalid full batch committed: %v", err)
			}
			invalid := 0
			for _, a := range terminal.ContentAudit {
				if a.Kind == ContentReceipts && a.Reason == ContentInvalid {
					invalid++
				}
			}
			if invalid != 2 {
				t.Fatalf("invalid sources were not recorded: %+v", terminal.ContentAudit)
			}
		})
	}
}

func TestReceiptsEmptyTransactionsRequireEmptyReceiptCommitment(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1)
	f.head.ReceiptHash = common.HexToHash("0x123")
	f = rebindReceiptBlock(t, f)
	lookup := receiptLookup(t, f)
	s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup, lookup)
	pushLogsHead(feeds, f.head)
	if _, err := s.Next(context.Background()); err == nil {
		t.Fatal("contradictory empty block roots committed")
	}
	if _, v := s.CommittedTip(); v != 0 {
		t.Fatal("contradictory block advanced path")
	}
}

func TestReceiptsRetriesNeverCombineOldVotes(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	lookup := receiptLookup(t, f)
	calls := make([]func(context.Context, string, ...any) rpc.Response, 2)
	for i := range calls {
		var count atomic.Int32
		calls[i] = func(ctx context.Context, method string, args ...any) rpc.Response {
			if method == "eth_getBlockReceipts" && int(count.Add(1)) != i+1 {
				return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("source unavailable this attempt")}}
			}
			return lookup(ctx, method, args...)
		}
	}
	p := testReceiptsPolicy()
	p.MaxRecoveryAttempts = 2
	p.RetryDelay = time.Millisecond
	s, feeds := startTestReceipts(t, context.Background(), nil, p, calls...)
	pushLogsHead(feeds, f.head)
	_, err := s.Next(context.Background())
	if !errors.Is(err, ErrRecoveryExhausted) {
		t.Fatalf("cross-attempt votes reached Q: %v", err)
	}
	if _, v := s.CommittedTip(); v != 0 {
		t.Fatal("retry combination advanced path")
	}
}

func TestReceiptsFixedDenominatorIncludesUnavailableMembers(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	lookup := receiptLookup(t, f)
	unavailable := func(ctx context.Context, method string, args ...any) rpc.Response {
		if method == "eth_getBlockReceipts" {
			return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("unavailable")}}
		}
		return lookup(ctx, method, args...)
	}
	s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup, unavailable, unavailable)
	pushLogsHead(feeds, f.head)
	if _, err := s.Next(context.Background()); err == nil {
		t.Fatal("one surviving answer shrank denominator")
	}
	forwarded, ff := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup)
	pushLogsHead(ff, f.head)
	proof := nextReceipts(t, forwarded).Path.Appended[0].ReceiptSupport
	if proof.Status != rpc.Forwarded || proof.Basis != 1 || len(proof.Suppliers) != 1 {
		t.Fatalf("single-source claim: %+v", proof)
	}
}

func TestReceiptsSlowMinorityDoesNotDelayQButCloseJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := makeLogsBlock(t, nil, 100, 1, 1)
		lookup := receiptLookup(t, f)
		started, release := make(chan struct{}), make(chan struct{})
		startOnce, releaseOnce := sync.OnceFunc(func() { close(started) }), sync.OnceFunc(func() { close(release) })
		fast := func(ctx context.Context, method string, args ...any) rpc.Response {
			if method == "eth_getBlockReceipts" {
				<-started
			}
			return lookup(ctx, method, args...)
		}
		slow := func(ctx context.Context, method string, args ...any) rpc.Response {
			if method == "eth_getBlockReceipts" {
				startOnce()
				<-release
			}
			return lookup(ctx, method, args...)
		}
		s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), fast, fast, slow)
		t.Cleanup(releaseOnce)
		pushLogsHead(feeds, f.head)
		e := nextReceipts(t, s)
		if len(e.Path.Appended[0].ReceiptSupport.Suppliers) != 2 {
			t.Fatal("quorum not reached")
		}
		closed := make(chan struct{})
		go func() { _ = s.Close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned before actual minority exit")
		default:
		}
		if len(s.slots) == 0 {
			t.Fatal("cancellation returned occupied supplier slot")
		}
		releaseOnce()
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("Close did not join")
		}
		select {
		case <-s.watcherDone:
		default:
			t.Fatal("Close did not join owner watcher")
		}
		if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
			t.Fatalf("close leaked content=%d head=%d", s.budget.Used(), s.head.budget.Used())
		}
	})
}

func TestReceiptsReorgPreBirthAndReentryRemainAtomic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		common := makeLogsBlock(t, nil, 98, 1)
		old99 := makeLogsBlock(t, common.head, 99, 1, 1)
		old100 := makeLogsBlock(t, old99.head, 100, 1, 1)
		alt99 := makeLogsBlock(t, common.head, 99, 2, 1)
		alt100 := makeLogsBlock(t, alt99.head, 100, 2, 1)
		last99 := makeLogsBlock(t, common.head, 99, 3, 1)
		lookup := receiptLookup(t, common, old99, old100, alt99, alt100, last99)
		var fetchGate chan struct{}
		gatedLookup := func(ctx context.Context, method string, args ...any) rpc.Response {
			select {
			case <-ctx.Done():
				return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: ctx.Err()}}
			case <-fetchGate:
				return lookup(ctx, method, args...)
			}
		}
		s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), gatedLookup, gatedLookup)
		push := func(head *types.Header) {
			// Sequential feed sends can start recovery while the other source
			// still has its old tip. This fixture contains only the scripted
			// forks, so settle both new tips before answering recovery requests.
			synctest.Wait()
			fetchGate = make(chan struct{})
			pushLogsHead(feeds, head)
			synctest.Wait()
			close(fetchGate)
		}
		push(old100.head)
		birth := nextReceipts(t, s)
		if len(birth.Path.Appended) != 1 || birth.Path.Appended[0].Node.Number != 100 {
			t.Fatal("birth replayed earlier receipts")
		}
		push(alt100.head)
		replaced := nextReceipts(t, s)
		if replaced.Path.Reason != PreBirthReorg || len(replaced.Path.Appended) != 2 || replaced.Path.Appended[0].Node.Hash != alt99.head.Hash() || len(replaced.Path.Retracted) != 1 {
			t.Fatalf("prebirth: %+v", replaced)
		}
		push(last99.head)
		last := nextReceipts(t, s)
		if len(last.Path.Retracted) != 2 || last.Path.Retracted[1].Hash != alt99.head.Hash() {
			t.Fatalf("multi-block retraction: %+v", last)
		}
		push(alt100.head)
		reentry := nextReceipts(t, s)
		if len(reentry.Path.Appended) != 2 || reentry.Path.NewVersion != 4 || reentry.Path.BaseVersion != 3 {
			t.Fatalf("reentry: %+v", reentry)
		}
	})
}

func TestReceiptsContinuousConsumptionReturnsHistoricalBudgets(t *testing.T) {
	blocks := make([]logsBlockFixture, 35)
	var parent *types.Header
	for i := range blocks {
		blocks[i] = makeLogsBlock(t, parent, uint64(100+i), 1, 1)
		parent = blocks[i].head
	}
	lookup := receiptLookup(t, blocks...)
	p := testReceiptsPolicy()
	p.Head.RetainedNodes = 8
	s, feeds := startTestReceipts(t, context.Background(), nil, p, lookup, lookup)
	var baseline uint64
	for i, block := range blocks {
		pushLogsHead(feeds, block.head)
		nextReceipts(t, s)
		// No following block is offered until the current admitted workers exit.
		s.wg.Wait()
		if i == 0 {
			baseline = s.budget.Used()
		}
		if used := s.budget.Used(); used > baseline+4096 {
			t.Fatalf("retained historical receipts: block=%d baseline=%d current=%d", i, baseline, used)
		}
	}
	_ = s.Close()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatalf("close budgets: %d/%d", s.budget.Used(), s.head.budget.Used())
	}
}

func TestReceiptsQuorumComparesOnlySelectedCanonicalRows(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1, 1)
	lookup := receiptLookup(t, f)
	f.receipts[1]["from"] = common.HexToAddress("0xab").Hex()
	other := receiptLookup(t, f)
	hashes := []ethapi.Hash{ethapi.Hash(f.txs[0].Hash().Hex())}
	s, feeds := startTestReceipts(t, context.Background(), &ethapi.TransactionReceiptsQuery{TransactionHashes: &hashes}, testReceiptsPolicy(), lookup, other)
	pushLogsHead(feeds, f.head)
	d := nextReceipts(t, s).Path.Appended[0]
	if len(d.Receipts) != 1 || d.Receipts[0].TransactionHash != hashes[0] || len(d.ReceiptSupport.Suppliers) != 2 {
		t.Fatalf("unselected declaration changed delivered quorum: %+v", d)
	}
}

func TestReceiptsFailureInLaterBlockDoesNotPublishPartialDelta(t *testing.T) {
	birth := makeLogsBlock(t, nil, 100, 1, 1)
	middle := makeLogsBlock(t, birth.head, 101, 1, 1)
	end := makeLogsBlock(t, middle.head, 102, 1, 1)
	end.receipts = nil
	lookup := receiptLookup(t, birth, middle, end)
	s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), lookup, lookup)
	pushLogsHead(feeds, birth.head)
	_ = nextReceipts(t, s)
	pushLogsHead(feeds, end.head)
	_, err := s.Next(context.Background())
	var terminal *ReceiptsTerminalError
	if !errors.As(err, &terminal) || terminal.PathVersion != 1 || terminal.CommittedTip.Hash != birth.head.Hash() {
		t.Fatalf("partial content delta became visible: %v", err)
	}
	if tip, version := s.CommittedTip(); version != 1 || tip.Hash != birth.head.Hash() {
		t.Fatalf("partial commit: %+v %d", tip, version)
	}
	_ = s.Close()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("partial delta retained proof ownership")
	}
}

func TestReceiptsDisconnectedSupplierCannotVoteDuringSlowUnsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := makeLogsBlock(t, nil, 100, 1, 1)
		lookup := receiptLookup(t, f)
		a, fa := newHeadSource("a")
		b, fb := newHeadSource("b")
		c, fc := newHeadSource("c")
		requested, returnContent := make(chan struct{}), make(chan struct{})
		cleanupStarted, returnCleanup := make(chan struct{}), make(chan struct{})
		releaseContent := sync.OnceFunc(func() { close(returnContent) })
		releaseCleanup := sync.OnceFunc(func() { close(returnCleanup) })
		fa.cleanup = sync.OnceFunc(func() { close(cleanupStarted); <-returnCleanup })
		p := testReceiptsPolicy()
		p.MaxConcurrentRequests = 1
		p.RequestTimeout = 0
		s, err := NewReceipts(context.Background(), context.Background(), func() ([]Source, error) {
			return []Source{
				&receiptTestSource{a, func(ctx context.Context, method string, args ...any) rpc.Response {
					if method == "eth_getBlockReceipts" {
						close(requested)
						<-returnContent
					}
					return lookup(ctx, method, args...)
				}},
				&receiptTestSource{b, lookup},
				&receiptTestSource{c, lookup},
			}, nil
		}, nil, p, rpc.CorroborationMajority)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { releaseContent(); releaseCleanup(); _ = s.Close() }()
		synctest.Wait()
		pushLogsHead([]*newHeadTestFeed{fa, fb, fc}, f.head)
		<-requested
		close(fa.events)
		close(fa.errs)
		<-cleanupStarted
		releaseContent()
		e := nextReceipts(t, s)
		for _, supplier := range e.Path.Appended[0].ReceiptSupport.Suppliers {
			if supplier.Session.SourceID == "a" {
				t.Fatal("retired receipt answer voted")
			}
		}
		for _, audit := range e.ContentAudit {
			if audit.Kind == ContentReceipts && audit.Session.SourceID == "a" && audit.Reason == ContentStaleSession {
				return
			}
		}
		t.Fatalf("retired receipt answer lost audit: %+v", e.ContentAudit)
	})
}

func TestReceiptsProducerTerminalPreservesFIFOAndOwnerCanDiscard(t *testing.T) {
	for _, discard := range []bool{false, true} {
		t.Run(fmt.Sprint(discard), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				first := makeLogsBlock(t, nil, 100, 1, 1)
				second := makeLogsBlock(t, first.head, 101, 1, 1)
				missing := makeLogsBlock(t, second.head, 102, 1, 1)
				owner, cancel := context.WithCancel(context.Background())
				defer cancel()
				s, feeds := startTestReceipts(t, owner, nil, testReceiptsPolicy(), receiptLookup(t, first, second))
				pushLogsHead(feeds, first.head)
				synctest.Wait()
				if _, v := s.CommittedTip(); v != 1 {
					t.Fatal("first delta did not commit")
				}
				pushLogsHead(feeds, second.head)
				synctest.Wait()
				if _, v := s.CommittedTip(); v != 2 {
					t.Fatal("second delta did not commit")
				}
				pushLogsHead(feeds, missing.head)
				<-s.done
				if s.sources != nil || s.active != nil || s.filter != nil {
					t.Fatal("finished producer retained work references")
				}
				if discard {
					cancel()
					synctest.Wait()
					if _, err := s.Next(context.Background()); !errors.Is(err, ErrClosed) {
						t.Fatalf("unread queue survived owner cancellation: %v", err)
					}
				} else {
					for i := uint64(1); i <= 2; i++ {
						e := nextReceipts(t, s)
						if e.Path.BaseVersion != i-1 || e.Path.NewVersion != i || e.Path.Appended[0].Node.Number != 99+i {
							t.Fatalf("FIFO lost: %+v", e.Path)
						}
					}
					_, err := s.Next(context.Background())
					var terminal *ReceiptsTerminalError
					if !errors.As(err, &terminal) || terminal.PathVersion != 2 || terminal.CommittedTip.Hash != second.head.Hash() || terminal.Audit.First == 0 {
						t.Fatalf("terminal: %v", err)
					}
					if _, err := s.Next(context.Background()); !errors.Is(err, ErrNextAfterTerminal) {
						t.Fatalf("repeated terminal: %v", err)
					}
					synctest.Wait()
				}
				if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
					t.Fatal("terminal delivery retained budget")
				}
			})
		})
	}
}

func TestReceiptsNextCancellationAndConcurrentCloseJoinActualWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := makeLogsBlock(t, nil, 100, 1, 1)
		lookup := receiptLookup(t, f)
		started, release := make(chan struct{}), make(chan struct{})
		releaseOnce := sync.OnceFunc(func() { close(release) })
		p := testReceiptsPolicy()
		p.RequestTimeout, p.RecoveryTimeout = 0, 0
		s, feeds := startTestReceipts(t, context.Background(), nil, p, func(ctx context.Context, method string, args ...any) rpc.Response {
			if method == "eth_getBlockReceipts" {
				close(started)
				<-release
			}
			return lookup(ctx, method, args...)
		})
		t.Cleanup(releaseOnce)
		pushLogsHead(feeds, f.head)
		<-started
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		nextDone := make(chan error, 1)
		go func() { _, err := s.Next(ctx); nextDone <- err }()
		closed := make(chan struct{}, 3)
		for range 3 {
			go func() { _ = s.Close(); closed <- struct{}{} }()
		}
		synctest.Wait()
		select {
		case err := <-nextDone:
			t.Fatalf("canceled Next escaped actual work: %v", err)
		default:
		}
		select {
		case <-closed:
			t.Fatal("Close escaped actual work")
		default:
		}
		releaseOnce()
		synctest.Wait()
		if err := <-nextDone; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		for range 3 {
			<-closed
		}
		if s.budget.Used() != 0 || s.head.budget.Used() != 0 || len(s.slots) != 0 {
			t.Fatal("canceled work retained ownership")
		}
	})
}

func TestReceiptsTerminalVisibleBeforeHungReceiptWorkerJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := makeLogsBlock(t, nil, 100, 1, 1)
		lookup := receiptLookup(t, f)
		started, release := make(chan struct{}), make(chan struct{})
		releaseOnce := sync.OnceFunc(func() { close(release) })
		p := testReceiptsPolicy()
		p.RequestTimeout = 0
		p.RecoveryTimeout = time.Second
		s, feeds := startTestReceipts(t, context.Background(), nil, p, func(ctx context.Context, method string, args ...any) rpc.Response {
			if method == "eth_getBlockReceipts" {
				close(started)
				<-release
			}
			return lookup(ctx, method, args...)
		})
		t.Cleanup(releaseOnce)
		pushLogsHead(feeds, f.head)
		<-started
		_, err := s.Next(context.Background())
		var terminal *ReceiptsTerminalError
		if !errors.As(err, &terminal) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("terminal: %v", err)
		}
		unfinished := false
		for _, audit := range terminal.ContentAudit {
			unfinished = unfinished || audit.Kind == ContentReceipts && audit.Reason == ContentRequestUnfinished
		}
		if !unfinished {
			t.Fatalf("terminal hid active receipt task: %+v", terminal.ContentAudit)
		}
		select {
		case <-s.done:
			t.Fatal("hung task joined early")
		default:
		}
		releaseOnce()
		_ = s.Close()
		if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
			t.Fatal("late task leaked ownership")
		}
	})
}

func TestReceiptsHeadProducerTerminalRetainsAudit(t *testing.T) {
	s, feeds := startTestReceipts(t, context.Background(), nil, testReceiptsPolicy(), func(context.Context, string, ...any) rpc.Response { return rpc.Response{} })
	feeds[0].events <- transport.Notification{Result: json.RawMessage(`{"hash":"bad"}`)}
	close(feeds[0].events)
	close(feeds[0].errs)
	_, err := s.Next(context.Background())
	var terminal *ReceiptsTerminalError
	var headTerminal *TerminalError
	if !errors.As(err, &terminal) || !errors.As(err, &headTerminal) || terminal.Audit.First == 0 {
		t.Fatalf("Head terminal audit: %v", err)
	}
	_ = s.Close()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("terminal leaked")
	}
}

type receiptsTestResolver struct {
	rpcengine.CanonicalOutcomeResolver
	resolve func(rpcengine.Corroboration, []rpcengine.CanonicalObservation) (rpcengine.Outcome, error)
}

func (r receiptsTestResolver) ResolveCanonicalCorroborated(rule rpcengine.Corroboration, observations []rpcengine.CanonicalObservation) (rpcengine.Outcome, error) {
	return r.resolve(rule, observations)
}

func TestReceiptsBudgetFailuresDoNotCommitOrLoseAudit(t *testing.T) {
	for _, stage := range []string{"head-transfer", "workspace", "candidate", "winner"} {
		t.Run(stage, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			lookup := receiptLookup(t, f)
			p := testReceiptsPolicy()
			p.MaxConcurrentRequests = 1
			p.RetryDelay, p.MaxRecoveryAttempts = time.Millisecond, 3
			if stage == "workspace" {
				p.MemoryBytes = 3 << 20
			}
			var s *ReceiptsSubscription
			reservations := make(chan func(), 1)
			fill := func() {
				hold, err := s.budget.Reserve(p.MemoryBytes - s.budget.Used() - 1)
				if err != nil {
					t.Error(err)
					return
				}
				reservations <- hold
			}
			call := func(ctx context.Context, method string, args ...any) rpc.Response {
				if stage == "candidate" && method == "eth_getBlockReceipts" {
					fill()
				}
				return lookup(ctx, method, args...)
			}
			var feeds []*newHeadTestFeed
			s, feeds = startTestReceipts(t, context.Background(), nil, p, call)
			if stage == "head-transfer" {
				fill()
			}
			if stage == "winner" {
				base := s.resolver
				s.resolver = receiptsTestResolver{base, func(rule rpcengine.Corroboration, observations []rpcengine.CanonicalObservation) (rpcengine.Outcome, error) {
					outcome, err := base.ResolveCanonicalCorroborated(rule, observations)
					if err == nil {
						fill()
					}
					return outcome, err
				}}
			}
			pushLogsHead(feeds, f.head)
			_, err := s.Next(context.Background())
			var terminal *ReceiptsTerminalError
			if !errors.As(err, &terminal) || !errors.Is(err, ErrMemoryBudget) || terminal.Audit.First == 0 || terminal.PathVersion != 0 {
				t.Fatalf("failure lost evidence or advanced path: %v", err)
			}
			if stage == "head-transfer" {
				var transfer *TransferError
				if !errors.As(err, &transfer) {
					t.Fatalf("expected transfer failure: %v", err)
				}
			}
			if stage == "candidate" || stage == "winner" {
				found := false
				for _, audit := range terminal.ContentAudit {
					if audit.Kind == ContentReceipts {
						found = true
						if audit.Attempt != 1 {
							t.Fatal("budget failure retried")
						}
					}
				}
				if !found {
					t.Fatal("receipt budget failure lost request audit")
				}
			}
			_ = s.Close()
			select {
			case release := <-reservations:
				release()
			default:
			}
			if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
				t.Fatal("failed stage retained budget")
			}
		})
	}
}

func TestReceiptsOversizeAnswerAndInternalResolverFailureRemainDistinct(t *testing.T) {
	for _, internal := range []bool{false, true} {
		t.Run(fmt.Sprint(internal), func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			lookup := receiptLookup(t, f)
			p := testReceiptsPolicy()
			var receiptCalls atomic.Int32
			if internal {
				p.RetryDelay, p.MaxRecoveryAttempts = time.Millisecond, 3
			}
			s, feeds := startTestReceipts(t, context.Background(), nil, p, func(ctx context.Context, method string, args ...any) rpc.Response {
				response := lookup(ctx, method, args...)
				if method == "eth_getBlockReceipts" {
					receiptCalls.Add(1)
					if !internal {
						response.Answer.Result = append(response.Answer.Result, strings.Repeat(" ", int(p.MaxResponseBytes))...)
					}
				}
				return response
			})
			if internal {
				s.resolver = receiptsTestResolver{s.resolver, func(rpcengine.Corroboration, []rpcengine.CanonicalObservation) (rpcengine.Outcome, error) {
					return rpcengine.Outcome{}, rpc.ErrInvalidOutcome
				}}
			}
			pushLogsHead(feeds, f.head)
			_, err := s.Next(context.Background())
			var terminal *ReceiptsTerminalError
			if !errors.As(err, &terminal) || terminal.PathVersion != 0 {
				t.Fatal(err)
			}
			if internal {
				if !errors.Is(err, rpc.ErrInvalidOutcome) || receiptCalls.Load() != 1 {
					t.Fatalf("internal failure retried/downgraded: %v calls=%d", err, receiptCalls.Load())
				}
			} else {
				found := false
				for _, audit := range terminal.ContentAudit {
					found = found || audit.Kind == ContentReceipts && audit.Reason == ContentResponseTooLarge
				}
				if !found {
					t.Fatalf("oversize failure missing: %+v", terminal.ContentAudit)
				}
			}
			_ = s.Close()
			if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
				t.Fatal("failed answer retained budget")
			}
		})
	}
}
