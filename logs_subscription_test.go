package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum/core/types"

	subcore "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

func testLogsPolicy() LogsSubscriptionPolicy {
	return LogsSubscriptionPolicy{Head: testHeadPolicy(), MemoryBytes: 32 << 20, MaxResponseBytes: 1 << 14, MaxConcurrentRequests: 3, RequestTimeout: time.Second, RecoveryTimeout: 2 * time.Second}
}

func logsFixtureLookup(t testing.TB, blocks ...logsBlockFixture) func(context.Context, string, ...any) (json.RawMessage, error) {
	t.Helper()
	byHash := make(map[string]logsBlockFixture, len(blocks))
	receipts := make(map[string]json.RawMessage, len(blocks))
	for _, block := range blocks {
		byHash[block.head.Hash().Hex()] = block
		receipts[block.head.Hash().Hex()] = logsJSON(t, block.receipts)
	}
	return func(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
		if len(args) == 0 {
			return nil, errors.New("missing hash")
		}
		hash, ok := args[0].(string)
		if !ok {
			return nil, errors.New("non-hash request")
		}
		block, ok := byHash[hash]
		if !ok {
			return nil, fmt.Errorf("unknown block %s", hash)
		}
		switch method {
		case "eth_getBlockByHash":
			if len(args) > 1 && args[1] == false {
				return testHeader(block.head), nil
			}
			return block.block, nil
		case "eth_getBlockReceipts":
			return receipts[hash], nil
		}
		return nil, fmt.Errorf("unexpected method %s", method)
	}
}

func startTestLogs(t *testing.T, owner context.Context, query string, p LogsSubscriptionPolicy, lookup func(context.Context, string, ...any) (json.RawMessage, error)) (*LogsSubscription, []*newHeadTestFeed) {
	t.Helper()
	var sources []identifiedSubscriptionSource
	var feeds []*newHeadTestFeed
	for _, id := range []string{"a", "b"} {
		source, feed := newHeadSource(id)
		sources = append(sources, &headLookupSource{source, lookup})
		feeds = append(feeds, feed)
	}
	filter, err := compileLogsFilter(logsQuery(t, query))
	if err != nil {
		t.Fatal(err)
	}
	s, err := startLogsSubscription(owner, context.Background(), sources, filter, p, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, feeds
}

func pushLogsHead(feeds []*newHeadTestFeed, head *types.Header) {
	for _, feed := range feeds {
		feed.events <- transport.Notification{Result: testHeader(head)}
	}
}
func nextLogs(t *testing.T, s *LogsSubscription) LogsEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func waitLogs(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("logs state did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLogsStreamProvesSharedOmissionsZeroMatchAndGap(t *testing.T) {
	birth := makeLogsBlock(t, nil, 100, 1, 2, 1)
	middle := makeLogsBlock(t, birth.head, 101, 1, 0)
	end := makeLogsBlock(t, middle.head, 102, 1, 1)
	s, feeds := startTestLogs(t, context.Background(), `{}`, testLogsPolicy(), logsFixtureLookup(t, birth, middle, end))
	pushLogsHead(feeds, birth.head)
	e := nextLogs(t, s)
	if len(e.Path.Appended) != 1 || len(e.Path.Appended[0].Logs) != 3 || e.Path.Appended[0].Proof != LogsBlockCommitment || e.Path.NewVersion != 1 {
		t.Fatalf("birth: %+v", e)
	}
	if e.Path.Appended[0].ObjectSupplier.RequestID == 0 || len(e.Evaluation.Witnesses) != 2 {
		t.Fatal("missing independent path/content evidence")
	}
	pushLogsHead(feeds, end.head)
	e = nextLogs(t, s)
	if len(e.Path.Appended) != 2 || e.Path.Appended[0].Node.Number != 101 || e.Path.Appended[0].Logs == nil || len(e.Path.Appended[0].Logs) != 0 || len(e.Path.Appended[1].Logs) != 1 {
		t.Fatalf("gap: %+v", e)
	}
	if tip, v := s.CommittedTip(); tip.Hash != end.head.Hash() || v != e.Path.NewVersion {
		t.Fatalf("committed %v/%d", tip, v)
	}
}

func TestLogsStreamRetainsFIFOReorgAndReentry(t *testing.T) {
	base := makeLogsBlock(t, nil, 100, 1, 0)
	x := makeLogsBlock(t, base.head, 101, 1, 1)
	y := makeLogsBlock(t, base.head, 101, 2, 2)
	s, feeds := startTestLogs(t, context.Background(), `{}`, testLogsPolicy(), logsFixtureLookup(t, base, x, y))
	for i, block := range []logsBlockFixture{base, x, y, x} {
		pushLogsHead(feeds, block.head)
		version := uint64(i + 1)
		waitHead(t, s.head, func() bool { return s.head.tracker.State().PathVersion == version })
	}
	for i, block := range []logsBlockFixture{base, x, y, x} {
		e := nextLogs(t, s)
		if e.Path.BaseVersion != uint64(i) || e.Path.NewVersion != uint64(i+1) || len(e.Path.Appended) != 1 || e.Path.Appended[0].Node.Hash != block.head.Hash() {
			t.Fatalf("delta %d: %+v", i, e)
		}
		if i >= 2 && (len(e.Path.Retracted) != 1 || *e.Path.Appended[0].Logs[0].Removed) {
			t.Fatal("reorg lost exact block retraction")
		}
	}
}

func TestLogsStreamRangeHashAndBloomStillAdvance(t *testing.T) {
	base := makeLogsBlock(t, nil, 100, 1, 1)
	end := makeLogsBlock(t, base.head, 101, 1, 1)
	replacement := makeLogsBlock(t, base.head, 101, 2, 1)
	for _, query := range []string{`{"toBlock":"0x64"}`, fmt.Sprintf(`{"blockHash":%q}`, base.head.Hash().Hex()), `{"address":"0x2222222222222222222222222222222222222222"}`} {
		t.Run(query, func(t *testing.T) {
			var fullCalls atomic.Int32
			lookup := logsFixtureLookup(t, base, end, replacement)
			s, feeds := startTestLogs(t, context.Background(), query, testLogsPolicy(), func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
				if m == "eth_getBlockByHash" && args[1] == true {
					fullCalls.Add(1)
				}
				return lookup(ctx, m, args...)
			})
			pushLogsHead(feeds, base.head)
			first := nextLogs(t, s)
			pushLogsHead(feeds, end.head)
			second := nextLogs(t, s)
			pushLogsHead(feeds, replacement.head)
			third := nextLogs(t, s)
			if second.Path.Appended[0].Logs == nil || len(second.Path.Appended[0].Logs) != 0 || len(third.Path.Retracted) != 1 || third.Path.Retracted[0].Hash != end.head.Hash() {
				t.Fatal("range stopped progress/retraction")
			}
			if first.Path.Appended[0].Proof == LogsBloomExcluded && fullCalls.Load() != 0 {
				t.Fatal("negative bloom fetched content")
			}
		})
	}
}

func TestLogsStreamInvalidSupplierAuditedBeforeValidFallback(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	valid := logsFixtureLookup(t, f)
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	filter, _ := compileLogsFilter(logsQuery(t, `{}`))
	p := testLogsPolicy()
	p.MaxConcurrentRequests = 1
	s, err := startLogsSubscription(context.Background(), context.Background(), []identifiedSubscriptionSource{&headLookupSource{a, func(context.Context, string, ...any) (json.RawMessage, error) { return json.RawMessage(`null`), nil }}, &headLookupSource{b, valid}}, filter, p, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	pushLogsHead([]*newHeadTestFeed{fa, fb}, f.head)
	e := nextLogs(t, s)
	if len(e.Path.Appended[0].Logs) != 1 || e.Path.Appended[0].ContentSource != "b" {
		t.Fatalf("fallback: %+v", e)
	}
	seen := false
	for _, audit := range e.ContentAudit {
		if audit.Session.SourceID == "a" && audit.Reason == ContentInvalid && audit.RequestID > 0 {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("bad supplier not audited: %+v", e.ContentAudit)
	}
}

func TestLogsStreamSlowMinorityDoesNotDelayDeliveryButCloseJoins(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	startOnce, cancelOnce, releaseOnce := sync.OnceFunc(func() { close(started) }), sync.OnceFunc(func() { close(canceled) }), sync.OnceFunc(func() { close(release) })
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	c, _ := newHeadSource("c")
	valid := logsFixtureLookup(t, f)
	fast := func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
		<-started
		return valid(ctx, m, args...)
	}
	slow := func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
		startOnce()
		<-ctx.Done()
		cancelOnce()
		<-release
		return nil, ctx.Err()
	}
	filter, _ := compileLogsFilter(logsQuery(t, `{}`))
	p := testLogsPolicy()
	s, err := startLogsSubscription(context.Background(), context.Background(), []identifiedSubscriptionSource{&headLookupSource{a, fast}, &headLookupSource{b, fast}, &headLookupSource{c, slow}}, filter, p, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseOnce(); _ = s.Close() })
	waitLogs(t, func() bool { _, err := s.head.sessions.Current("c"); return err == nil })
	pushLogsHead([]*newHeadTestFeed{fa, fb}, f.head)
	e := nextLogs(t, s)
	if len(e.Path.Appended[0].Logs) != 1 {
		t.Fatal("missing log")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("minority not canceled")
	}
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("Close failed to join minority")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatalf("close budgets logs=%d head=%d", s.budget.Used(), s.head.budget.Used())
	}
}

func TestLogsStreamTerminalVisibleBeforeHungSupplierJoin(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	started, release := make(chan struct{}), make(chan struct{})
	startOnce, releaseOnce := sync.OnceFunc(func() { close(started) }), sync.OnceFunc(func() { close(release) })
	p := testLogsPolicy()
	p.RecoveryTimeout = 30 * time.Millisecond
	p.RequestTimeout = 0
	s, feeds := startTestLogs(t, context.Background(), `{}`, p, func(context.Context, string, ...any) (json.RawMessage, error) {
		startOnce()
		<-release
		return nil, errors.New("late")
	})
	t.Cleanup(releaseOnce)
	pushLogsHead(feeds, f.head)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Next(ctx)
	var terminal *LogsTerminalError
	if !errors.As(err, &terminal) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("terminal: %v", err)
	}
	if terminal.Audit.First == 0 || len(terminal.ContentAudit) != 2 {
		t.Fatalf("terminal lost audit: %+v", terminal)
	}
	for _, a := range terminal.ContentAudit {
		if a.Reason != ContentRequestUnfinished {
			t.Fatal(a)
		}
	}
	select {
	case <-s.done:
		t.Fatal("supplier already joined")
	default:
	}
	releaseOnce()
	_ = s.Close()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("terminal leaked budget")
	}
}

func TestLogsOwnerCloseAfterProducerEndDiscardsUnreadEvents(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	next := makeLogsBlock(t, f.head, 101, 1, 1)
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	valid := logsFixtureLookup(t, f)
	s, feeds := startTestLogs(t, owner, `{}`, testLogsPolicy(), valid)
	pushLogsHead(feeds, f.head)
	waitLogs(t, func() bool { _, v := s.CommittedTip(); return v == 1 })
	pushLogsHead(feeds, next.head)
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("producer did not terminate")
	}
	cancel()
	waitLogs(t, func() bool { return s.budget.Used() == 0 })
	if _, err := s.Next(context.Background()); !errors.Is(err, subcore.ErrClosed) {
		t.Fatalf("owner close did not discard queue: %v", err)
	}
}

func TestLogsStreamBudgetReturnsWithoutHistoricalPayloads(t *testing.T) {
	var blocks []logsBlockFixture
	var parent *types.Header
	for i := uint64(100); i < 130; i++ {
		block := makeLogsBlock(t, parent, i, 1, 1)
		blocks = append(blocks, block)
		parent = block.head
	}
	s, feeds := startTestLogs(t, context.Background(), `{}`, testLogsPolicy(), logsFixtureLookup(t, blocks...))
	var baseline uint64
	for i, block := range blocks {
		pushLogsHead(feeds, block.head)
		_ = nextLogs(t, s)
		// This delta has been consumed and the next block has not been sent,
		// so the producer has no new content task to register during Wait.
		// A released slot alone is insufficient: worker defers return the
		// shared Header lease after returning the slot, then signal Done.
		s.wg.Wait()
		s.mu.Lock()
		active := len(s.active)
		s.mu.Unlock()
		if active != 0 || len(s.slots) != 0 {
			t.Fatalf("completed block retained requests: active=%d slots=%d", active, len(s.slots))
		}
		used := s.budget.Used()
		if i == 0 {
			baseline = used
		}
		if used > baseline+4096 {
			t.Fatalf("historical memory grew: initial=%d current=%d block=%d", baseline, used, i)
		}
	}
	_ = s.Close()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("close leaked")
	}
}

func TestLogsNextCancellationAndInvalidPolicies(t *testing.T) {
	s, _ := startTestLogs(t, context.Background(), `{}`, testLogsPolicy(), func(context.Context, string, ...any) (json.RawMessage, error) { return nil, errors.New("unused") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("Next cancellation did not join/cleanup")
	}
	for _, edit := range []func(*LogsSubscriptionPolicy){func(p *LogsSubscriptionPolicy) { p.MemoryBytes = 0 }, func(p *LogsSubscriptionPolicy) { p.MaxResponseBytes = 0 }, func(p *LogsSubscriptionPolicy) { p.MaxConcurrentRequests = 0 }, func(p *LogsSubscriptionPolicy) { p.RequestTimeout = -1 }} {
		p := testLogsPolicy()
		edit(&p)
		if validateLogsPolicy(p) == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}

func TestLogsPreBirthReorgIncludesNewLowPathAndLaterRetraction(t *testing.T) {
	common := makeLogsBlock(t, nil, 98, 1, 0)
	old99 := makeLogsBlock(t, common.head, 99, 1, 1)
	old100 := makeLogsBlock(t, old99.head, 100, 1, 1)
	alt99 := makeLogsBlock(t, common.head, 99, 2, 1)
	alt100 := makeLogsBlock(t, alt99.head, 100, 2, 1)
	last99 := makeLogsBlock(t, common.head, 99, 3, 1)
	s, feeds := startTestLogs(t, context.Background(), `{"fromBlock":"latest"}`, testLogsPolicy(), logsFixtureLookup(t, common, old99, old100, alt99, alt100, last99))
	pushLogsHead(feeds, old100.head)
	birth := nextLogs(t, s)
	if len(birth.Path.Appended) != 1 || birth.Path.Appended[0].Node.Number != 100 {
		t.Fatal("historical replay before birth")
	}
	pushLogsHead(feeds, alt100.head)
	replacement := nextLogs(t, s)
	if replacement.Path.Reason != HeadPreBirthReorg || len(replacement.Path.Appended) != 2 || replacement.Path.Appended[0].Node.Hash != alt99.head.Hash() || len(replacement.Path.Appended[0].Logs) != 1 {
		t.Fatalf("prebirth replacement: %+v", replacement)
	}
	pushLogsHead(feeds, last99.head)
	last := nextLogs(t, s)
	if len(last.Path.Retracted) != 2 || last.Path.Retracted[1].Hash != alt99.head.Hash() || len(last.Path.Appended) != 1 || last.Path.Appended[0].Node.Hash != last99.head.Hash() {
		t.Fatalf("lost low retraction: %+v", last)
	}
}

func TestLogsLateSessionAndTimeoutCannotCommit(t *testing.T) {
	for _, mode := range []string{"session", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			valid := logsFixtureLookup(t, f)
			started, release := make(chan struct{}), make(chan struct{})
			startOnce, releaseOnce := sync.OnceFunc(func() { close(started) }), sync.OnceFunc(func() { close(release) })
			p := testLogsPolicy()
			p.MaxConcurrentRequests = 1
			p.RequestTimeout = 20 * time.Millisecond
			var source *LogsSubscription
			s, feeds := startTestLogs(t, context.Background(), `{}`, p, func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
				startOnce()
				<-release
				return valid(context.Background(), m, args...)
			})
			source = s
			t.Cleanup(releaseOnce)
			pushLogsHead(feeds, f.head)
			<-started
			if mode == "session" {
				_, err := source.head.sessions.Open("a")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				time.Sleep(30 * time.Millisecond)
			}
			releaseOnce()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// b remains a valid independent supplier and may complete after a's
			// stale/expired answer; that answer itself must not be selected.
			e, err := s.Next(ctx)
			var audits []ContentAudit
			if err == nil {
				if e.Path.Appended[0].ContentSource == "a" {
					t.Fatal("late supplier committed")
				}
				audits = e.ContentAudit
			} else {
				var terminal *LogsTerminalError
				if !errors.As(err, &terminal) {
					t.Fatal(err)
				}
				audits = terminal.ContentAudit
			}
			want := ContentStaleSession
			if mode == "timeout" {
				want = ContentRequestTimeout
			}
			found := false
			for _, a := range audits {
				if a.Session.SourceID == "a" && a.Reason == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing late reason %d: %+v", want, audits)
			}
		})
	}
}

func TestLogsDisconnectedSupplierCannotCommitDuringSlowUnsubscribe(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	lookup := logsFixtureLookup(t, f)
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	requested, returnContent := make(chan struct{}), make(chan struct{})
	cleanupStarted, returnCleanup := make(chan struct{}), make(chan struct{})
	releaseContent := sync.OnceFunc(func() { close(returnContent) })
	releaseCleanup := sync.OnceFunc(func() { close(returnCleanup) })
	fa.cleanup = sync.OnceFunc(func() { close(cleanupStarted); <-returnCleanup })
	filter, err := compileLogsFilter(logsQuery(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	p := testLogsPolicy()
	p.MaxConcurrentRequests = 1
	p.RequestTimeout = 0
	started := sync.OnceFunc(func() { close(requested) })
	s, err := startLogsSubscription(context.Background(), context.Background(), []identifiedSubscriptionSource{
		&headLookupSource{a, func(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
			started()
			<-returnContent
			return lookup(ctx, method, args...)
		}},
		&headLookupSource{b, lookup},
	}, filter, p, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseContent(); releaseCleanup(); _ = s.Close() })
	pushLogsHead([]*newHeadTestFeed{fa, fb}, f.head)
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("content request did not start")
	}
	close(fa.events)
	close(fa.errs)
	select {
	case <-cleanupStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected source did not enter cleanup")
	}
	releaseContent()
	e := nextLogs(t, s)
	if e.Path.Appended[0].ContentSource != "b" {
		t.Fatal("disconnected source committed while its unsubscribe was blocked")
	}
	for _, audit := range e.ContentAudit {
		if audit.Session.SourceID == "a" && audit.Reason == ContentStaleSession {
			return
		}
	}
	t.Fatalf("retired content answer was not audited: %+v", e.ContentAudit)
}

func TestLogsHeadTransferBudgetFailurePreservesAudit(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	s, feeds := startTestLogs(t, context.Background(), `{}`, testLogsPolicy(), logsFixtureLookup(t, f))
	hold, err := s.budget.Reserve(s.policy.MemoryBytes - s.budget.Used() - 1)
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	pushLogsHead(feeds, f.head)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = s.Next(ctx)
	var terminal *LogsTerminalError
	var transfer *subcore.TransferError
	if !errors.As(err, &terminal) || !errors.As(err, &transfer) || !errors.Is(err, subcore.ErrMemoryBudget) || terminal.Audit.First == 0 {
		t.Fatalf("budget terminal lost Head audit: %v", err)
	}
	_ = s.Close()
	hold()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("failed transfer leaked source audit")
	}
}

func TestLogsOversizeAndContentBudgetFailuresKeepAudit(t *testing.T) {
	for _, mode := range []string{"response", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			p := testLogsPolicy()
			p.MaxConcurrentRequests = 1
			if mode == "response" {
				p.MaxResponseBytes = 128
			} else {
				p.MemoryBytes = 3 << 20
			}
			s, feeds := startTestLogs(t, context.Background(), `{}`, p, logsFixtureLookup(t, f))
			pushLogsHead(feeds, f.head)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := s.Next(ctx)
			var terminal *LogsTerminalError
			if !errors.As(err, &terminal) || terminal.Audit.First == 0 {
				t.Fatalf("missing failure audit: %v", err)
			}
			if mode == "response" {
				found := false
				for _, a := range terminal.ContentAudit {
					found = found || a.Reason == ContentResponseTooLarge
				}
				if !found {
					t.Fatal("oversize not audited")
				}
			} else if !errors.Is(err, subcore.ErrMemoryBudget) {
				t.Fatalf("workspace result=%v", err)
			}
			_ = s.Close()
			if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
				t.Fatal("content failure leaked")
			}
		})
	}
}

func TestLogsLateCompletionAuditAppearsInFollowingDelta(t *testing.T) {
	base := makeLogsBlock(t, nil, 100, 1, 1)
	next := makeLogsBlock(t, base.head, 101, 1, 1)
	started, release := make(chan struct{}), make(chan struct{})
	startOnce, releaseOnce := sync.OnceFunc(func() { close(started) }), sync.OnceFunc(func() { close(release) })
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	c, _ := newHeadSource("c")
	valid := logsFixtureLookup(t, base, next)
	fast := func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
		<-started
		return valid(ctx, m, args...)
	}
	slow := func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
		startOnce()
		<-release
		return nil, ctx.Err()
	}
	filter, _ := compileLogsFilter(logsQuery(t, `{}`))
	s, err := startLogsSubscription(context.Background(), context.Background(), []identifiedSubscriptionSource{&headLookupSource{a, fast}, &headLookupSource{b, fast}, &headLookupSource{c, slow}}, filter, testLogsPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseOnce(); _ = s.Close() })
	waitLogs(t, func() bool { _, err := s.head.sessions.Current("c"); return err == nil })
	pushLogsHead([]*newHeadTestFeed{fa, fb}, base.head)
	_ = nextLogs(t, s)
	releaseOnce()
	waitLogs(t, func() bool { return len(s.slots) == 0 })
	pushLogsHead([]*newHeadTestFeed{fa, fb}, next.head)
	e := nextLogs(t, s)
	found := false
	for _, a := range e.ContentAudit {
		if a.Session.SourceID == "c" && a.Block.Hash == base.head.Hash() && a.Reason == ContentRequestCanceled {
			found = true
		}
	}
	if !found {
		t.Fatalf("late completion disappeared: %+v", e.ContentAudit)
	}
}

func TestLogsPublishBudgetFailureKeepsVersionAndAudits(t *testing.T) {
	budget := subcore.NewMemoryBudget(4096)
	terminalReserve, err := budget.Reserve(256)
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &LogsSubscription{contentSubscription: &contentSubscription[LogsEvent]{life: life, cancel: cancel, budget: budget, notify: make(chan struct{}, 1), drained: make(chan struct{}), terminalReserve: terminalReserve, committedTip: AnchorNode{Number: 100}, pathVersion: 1}}
	proofHold, err := budget.Reserve(3000)
	if err != nil {
		t.Fatal(err)
	}
	auditHold, err := budget.Reserve(512)
	if err != nil {
		t.Fatal(err)
	}
	s.auditFirst = &contentAuditEntry{record: ContentAudit{Sequence: 1, Reason: ContentInvalid}, release: auditHold}
	s.auditLast = s.auditFirst
	headAudit := HeadAuditRange{First: 9, Last: 10}
	e := LogsEvent{Path: LogsPathDelta{BaseVersion: 1, NewVersion: 2, Appended: []LogsDelivery{{Node: AnchorNode{Number: 101}, Logs: []ethapi.Log{}}}}, Audit: headAudit}
	if err := s.publish(e, []func(){proofHold}); !errors.Is(err, subcore.ErrMemoryBudget) {
		t.Fatalf("publish=%v", err)
	} else {
		s.finish(err, e.Audit, nil, []func(){proofHold})
	}
	if tip, v := s.CommittedTip(); tip.Number != 100 || v != 1 {
		t.Fatal("failed queue advanced path")
	}
	_, err = s.Next(context.Background())
	var terminal *LogsTerminalError
	if !errors.As(err, &terminal) || terminal.Audit.First != 9 || len(terminal.ContentAudit) != 1 || terminal.ContentAudit[0].Reason != ContentInvalid {
		t.Fatalf("lost publication failure evidence: %v", err)
	}
	if budget.Used() != 0 {
		t.Fatalf("publication failure leaked %d", budget.Used())
	}
}

func TestLogsHeadProducerTerminalRetainsItsAudit(t *testing.T) {
	s, feeds := startTestLogs(t, context.Background(), `{}`, testLogsPolicy(), func(context.Context, string, ...any) (json.RawMessage, error) { return nil, errors.New("unused") })
	feeds[0].events <- transport.Notification{Result: json.RawMessage(`{"hash":"bad"}`)}
	close(feeds[0].events)
	close(feeds[0].errs)
	close(feeds[1].events)
	close(feeds[1].errs)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Next(ctx)
	var terminal *LogsTerminalError
	var headTerminal *HeadTerminalError
	if !errors.As(err, &terminal) || !errors.As(err, &headTerminal) || terminal.Audit.First == 0 {
		t.Fatalf("lost Head producer audit: %v", err)
	}
	count := 0
	for range terminal.Audit.All() {
		count++
	}
	if count == 0 {
		t.Fatal("empty Head terminal audit")
	}
	_ = s.Close()
	if s.budget.Used() != 0 || s.head.budget.Used() != 0 {
		t.Fatal("producer terminal leaked")
	}
}

func TestLogsCloseDropsLargeFrozenFilter(t *testing.T) {
	addresses := make([]string, 1024)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("0x%040x", i)
	}
	query := string(logsJSON(t, map[string]any{"address": addresses}))
	s, _ := startTestLogs(t, context.Background(), query, testLogsPolicy(), func(context.Context, string, ...any) (json.RawMessage, error) { return nil, errors.New("unused") })
	_ = s.Close()
	if s.filter.addresses != nil || s.filter.topics != nil || s.active != nil || s.sources != nil || s.budget.Used() != 0 {
		t.Fatal("closed handle retains input payload")
	}
}
