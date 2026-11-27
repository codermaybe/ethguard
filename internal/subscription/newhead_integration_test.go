package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

type newHeadTestFeed struct {
	events  chan transport.Notification
	errs    chan error
	cleanup func()
}

func (f *newHeadTestFeed) Events() <-chan transport.Notification { return f.events }
func (f *newHeadTestFeed) Err() <-chan error                     { return f.errs }
func (f *newHeadTestFeed) Unsubscribe() {
	if f.cleanup != nil {
		f.cleanup()
	}
}

type newHeadTestSource struct {
	id   string
	feed *newHeadTestFeed
}

func (s *newHeadTestSource) ID() string { return s.id }
func (s *newHeadTestSource) Subscribe(context.Context, string, ...any) (transport.Subscription, error) {
	return s.feed, nil
}
func (s *newHeadTestSource) SubscribeBounded(ctx context.Context, _ transport.NotificationPolicy, kind string, args ...any) (transport.Subscription, error) {
	return s.Subscribe(ctx, kind, args...)
}

func testHeader(block *types.Header) json.RawMessage {
	raw, _ := json.Marshal(block)
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	object["hash"] = json.RawMessage(fmt.Sprintf("%q", block.Hash().Hex()))
	raw, _ = json.Marshal(object)
	return raw
}

func newHeadSource(id string) (*newHeadTestSource, *newHeadTestFeed) {
	feed := &newHeadTestFeed{events: make(chan transport.Notification, 8), errs: make(chan error)}
	return &newHeadTestSource{id: id, feed: feed}, feed
}

func testHeadPolicy() HeadSubscriptionPolicy {
	return HeadSubscriptionPolicy{MemoryBytes: 8 << 20, RetainedNodes: 16, MaxConcurrentRequests: 8, WireMessages: 8, WireBytes: 1 << 16, MaxMessageBytes: 1 << 14}
}

func TestHeadFixedBudgetArithmeticBoundaries(t *testing.T) {
	base := uint64(unsafe.Sizeof(HeadSubscription{})) + 16384
	maximumSources := (^uint64(0) - base) / 4608
	for _, sources := range []uint64{0, 1, 3, maximumSources, maximumSources + 1, 1 << 55} {
		bytes, err := headSubscriptionFixedBytes(sources)
		if sources > maximumSources {
			if !errors.Is(err, ErrMemoryBudget) || bytes != 0 {
				t.Fatalf("synthetic source count %d: bytes=%d error=%v", sources, bytes, err)
			}
		} else if err != nil || bytes != base+sources*4608 {
			t.Fatalf("source count %d changed its fixed budget: bytes=%d error=%v", sources, bytes, err)
		}
	}
}

func TestNewHeadQuorumDeduplicatesAndReplacesSourceTips(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	sub, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	parent := &types.Header{Number: big.NewInt(99), Difficulty: big.NewInt(1)}
	child := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1), ParentHash: parent.Hash()}
	fa.events <- transport.Notification{Result: testHeader(child)}
	fb.events <- transport.Notification{Result: testHeader(child)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e, err := sub.Next(ctx)
	if err != nil || len(e.Path.Appended) != 1 || e.Path.NewVersion != 1 {
		t.Fatalf("initial %+v %v", e, err)
	}
	next := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: child.Hash()}
	fa.events <- transport.Notification{Result: testHeader(next)}
	fb.events <- transport.Notification{Result: testHeader(next)}
	if _, err = sub.Next(ctx); err != nil {
		t.Fatal(err)
	}
	alt := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(2), ParentHash: child.Hash()}
	fa.events <- transport.Notification{Result: testHeader(alt)}
	fb.events <- transport.Notification{Result: testHeader(alt)}
	e, err = sub.Next(ctx)
	if err != nil || len(e.Path.Retracted) != 1 || len(e.Path.Appended) != 1 || e.Path.BaseVersion != 2 {
		t.Fatalf("reorg %+v %v", e, err)
	}
}
func TestNewHeadOwnerCancelDoesNotPublishIntentionalCloseError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a, _ := newHeadSource("a")
	b, _ := newHeadSource("b")
	s, err := startNewHeadSubscription(ctx, context.Background(), []Source{a, b}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	read, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	if _, err = s.Next(read); err == nil {
		t.Fatal("owner close returned event")
	}
}

type headLookupSource struct {
	*newHeadTestSource
	lookup func(context.Context, string, ...any) (json.RawMessage, error)
}

func (s *headLookupSource) CallContext(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
	return s.lookup(ctx, m, args...)
}
func TestHeadAncestorNeedsOneObjectAndReturnsAtomicGapDelta(t *testing.T) {
	for _, enough := range []bool{false, true} {
		t.Run(fmt.Sprint(enough), func(t *testing.T) {
			a, fa := newHeadSource("a")
			b, fb := newHeadSource("b")
			n100 := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
			n101 := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: n100.Hash()}
			n102 := &types.Header{Number: big.NewInt(102), Difficulty: big.NewInt(1), ParentHash: n101.Hash()}
			lookup := func(fail bool) func(context.Context, string, ...any) (json.RawMessage, error) {
				return func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
					if m == "eth_getBlockByHash" {
						if fail {
							return nil, errors.New("missing ancestor")
						}
						return testHeader(n101), nil
					}
					return nil, errors.New("no height probe")
				}
			}
			policy := testHeadPolicy()
			policy.RecoveryTimeout = time.Second
			s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{&headLookupSource{a, lookup(false)}, &headLookupSource{b, lookup(!enough)}}, policy, rpc.CorroborationMajority)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			fa.events <- transport.Notification{Result: testHeader(n100)}
			fb.events <- transport.Notification{Result: testHeader(n100)}
			if _, err = s.Next(ctx); err != nil {
				t.Fatal(err)
			}
			fa.events <- transport.Notification{Result: testHeader(n102)}
			fb.events <- transport.Notification{Result: testHeader(n102)}
			event, err := s.Next(ctx)
			if err != nil || len(event.Path.Appended) != 2 || event.Path.Appended[0].Node.Number != 101 || event.Path.Appended[1].Node.Number != 102 {
				t.Fatalf("gap %+v %v", event, err)
			}
			if len(event.Evaluation.Witnesses) != 2 || event.Path.Appended[0].ObjectSupplier.Session.SourceID == "" {
				t.Fatal("missing path witnesses or independent object supplier")
			}
		})
	}
}

func waitHead(t *testing.T, s *HeadSubscription, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		ok := predicate()
		s.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("head state did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHeadOneObjectArrivalDrainsRecoveringAndNewerReadyTargets(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	chain := make(map[string]json.RawMessage)
	var headers []*types.Header
	for i := int64(100); i <= 106; i++ {
		h := &types.Header{Number: big.NewInt(i), Difficulty: big.NewInt(1)}
		if len(headers) > 0 {
			h.ParentHash = headers[len(headers)-1].Hash()
		}
		headers = append(headers, h)
		chain[h.Hash().Hex()] = testHeader(h)
	}
	lastStarted, release := make(chan struct{}), make(chan struct{})
	lookup := func(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
		if method != "eth_getBlockByHash" {
			return nil, errors.New("unexpected lookup")
		}
		if args[0] == headers[1].Hash().Hex() {
			close(lastStarted)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return chain[args[0].(string)], nil
	}
	policy := testHeadPolicy()
	policy.MaxConcurrentRequests = 1
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{&headLookupSource{a, lookup}, b}, policy, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, feed := range []*newHeadTestFeed{fa, fb} {
		feed.events <- transport.Notification{Result: testHeader(headers[0])}
	}
	if _, err := s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	for _, feed := range []*newHeadTestFeed{fa, fb} {
		feed.events <- transport.Notification{Result: testHeader(headers[5])}
	}
	select {
	case <-lastStarted:
	case <-ctx.Done():
		t.Fatal("recovery did not reach final missing object")
	}
	for _, feed := range []*newHeadTestFeed{fa, fb} {
		feed.events <- transport.Notification{Result: testHeader(headers[6])}
	}
	waitHead(t, s, func() bool {
		e, err := s.tracker.Evaluate()
		return err == nil && e.Target != nil && e.Target.Number == 106
	})
	close(release)
	for _, target := range []uint64{105, 106} {
		e, err := s.Next(ctx)
		if err != nil || len(e.Path.Appended) == 0 || e.Path.Appended[len(e.Path.Appended)-1].Node.Number != target {
			t.Fatalf("ready target %d stalled: %+v %v", target, e, err)
		}
	}
}
func TestHeadProducerFailureDrainsMultipleDeltasAndCloseReleases(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	var parent [32]byte
	for i := int64(100); i < 103; i++ {
		h := &types.Header{Number: big.NewInt(i), Difficulty: big.NewInt(1), ParentHash: parent}
		parent = h.Hash()
		fa.events <- transport.Notification{Result: testHeader(h)}
		fb.events <- transport.Notification{Result: testHeader(h)}
		// Wait for publication without consuming the queue.
		deadline := time.Now().Add(time.Second)
		for {
			s.mu.Lock()
			v := s.tracker.State().PathVersion
			s.mu.Unlock()
			if v == uint64(i-99) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("delta not committed")
			}
			time.Sleep(time.Millisecond)
		}
	}
	boom := errors.New("producer stopped")
	s.mu.Lock()
	s.finishLocked(boom)
	s.mu.Unlock()
	for i := uint64(1); i <= 3; i++ {
		e, err := s.Next(ctx)
		if err != nil || e.Path.NewVersion != i {
			t.Fatalf("FIFO %d %+v %v", i, e, err)
		}
	}
	if _, err = s.Next(ctx); !errors.Is(err, boom) {
		t.Fatalf("terminal %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := s.budget.Used(); n != 0 {
		t.Fatalf("close retained %d bytes", n)
	}
}
func TestHeadOpenReservesTerminalControlBeforeNetwork(t *testing.T) {
	a, _ := newHeadSource("a")
	b, _ := newHeadSource("b")
	policy := testHeadPolicy()
	policy.MemoryBytes = 16
	policy.WireBytes = 16
	policy.MaxMessageBytes = 16
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, policy, rpc.CorroborationMajority)
	if s != nil || !errors.Is(err, ErrMemoryBudget) {
		t.Fatalf("open admission %v %v", s, err)
	}
}

func TestHeadProducerFinishedThenOwnerCancelReleasesUnreadQueue(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	s, err := startNewHeadSubscription(owner, context.Background(), []Source{a, b}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	h := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	fa.events <- transport.Notification{Result: testHeader(h)}
	fb.events <- transport.Notification{Result: testHeader(h)}
	waitHead(t, s, func() bool { return s.tracker.State().PathVersion == 1 })
	close(fa.events)
	close(fa.errs)
	close(fb.events)
	close(fb.errs)
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("producers did not finish")
	}
	if s.budget.Used() == 0 {
		t.Fatal("test did not retain unread delivery and terminal")
	}
	select {
	case <-s.watcherDone:
		t.Fatal("owner watcher ended before unread delivery was drained or aborted")
	default:
	}
	cancel()
	select {
	case <-s.queue.Done():
	case <-time.After(time.Second):
		t.Fatal("owner stopped watching after producer completion")
	}
	if n := s.budget.Used(); n != 0 {
		t.Fatalf("owner cancellation retained %d bytes", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.watcherDone:
	default:
		t.Fatal("Close returned before owner watcher exited")
	}
}

func TestHeadDisconnectRevokesBeforeSlowCleanupAndHealthyQuorumContinues(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	c, fc := newHeadSource("c")
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	fa.cleanup = sync.OnceFunc(func() { close(started); <-release })
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b, c}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		unblock()
		_ = s.Close() // Cleanup joins subscription work; Close always returns nil.
	}()
	base := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	fa.events <- transport.Notification{Result: testHeader(base)}
	fb.events <- transport.Notification{Result: testHeader(base)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	close(fa.events)
	fa.errs <- transport.ErrNotificationBackpressure
	close(fa.errs)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("cleanup did not start")
	}
	s.mu.Lock()
	evaluation, evalErr := s.tracker.Evaluate()
	s.mu.Unlock()
	if evalErr != nil || evaluation.Target != nil {
		t.Fatalf("disconnected source kept its vote through cleanup: %+v %v", evaluation, evalErr)
	}
	next := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: base.Hash()}
	fb.events <- transport.Notification{Result: testHeader(next)}
	fc.events <- transport.Notification{Result: testHeader(next)}
	e, err := s.Next(ctx)
	if err != nil || e.Path.NewVersion != 2 {
		t.Fatalf("healthy quorum stopped: %+v %v", e, err)
	}
	found := false
	for record := range e.Audit.All() {
		if record.Session.SourceID == "a" && record.Kind == HeadSessionDisconnected && record.Reason == HeadNotificationLimit {
			found = true
		}
	}
	if !found {
		t.Fatal("notification overflow cause was lost")
	}
}

func TestHeadInvalidParentHeightRevokesOnlyThatSource(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	c, fc := newHeadSource("c")
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b, c}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	base := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	fa.events <- transport.Notification{Result: testHeader(base)}
	fb.events <- transport.Notification{Result: testHeader(base)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	invalid := &types.Header{Number: big.NewInt(500), Difficulty: big.NewInt(1), ParentHash: base.Hash()}
	fa.events <- transport.Notification{Result: testHeader(invalid)}
	waitHead(t, s, func() bool {
		e, _ := s.tracker.Evaluate()
		return e.Target == nil
	})
	next := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: base.Hash()}
	fb.events <- transport.Notification{Result: testHeader(next)}
	fc.events <- transport.Notification{Result: testHeader(next)}
	e, err := s.Next(ctx)
	if err != nil || e.Path.NewVersion != 2 {
		t.Fatalf("invalid source terminated healthy quorum: %+v %v", e, err)
	}
	found := false
	for record := range e.Audit.All() {
		if record.Session.SourceID == "a" && record.Reason == HeadInvalidPush {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid source revocation was not audited")
	}
}

func TestHeadDisconnectAuditAdmissionFailureTerminates(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	base := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	fa.events <- transport.Notification{Result: testHeader(base)}
	fb.events <- transport.Notification{Result: testHeader(base)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	release, err := s.budget.Reserve(s.policy.MemoryBytes - s.budget.Used())
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.disconnectNow(SourceSession{SourceID: "b", Generation: 1}, errors.New("lost"), false)
	release()
	if _, err := s.Next(ctx); !errors.Is(err, ErrHeadMemoryBudget) {
		t.Fatalf("disconnect audit failure disappeared: %v", err)
	}
}

func TestHeadRealHeaderLongAdvanceAndForkRetentionPlateaus(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	c, fc := newHeadSource("c")
	policy := testHeadPolicy()
	policy.RetainedNodes = 4
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b, c}, policy, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var parent [32]byte
	var peak uint64
	version := uint64(0)
	for height := int64(100); height < 600; height++ {
		h := &types.Header{Number: big.NewInt(height), Difficulty: big.NewInt(1), ParentHash: parent}
		if height == 100 {
			fc.events <- transport.Notification{Result: testHeader(h)}
		}
		variants := []*types.Header{h}
		if height%10 == 0 && height > 100 {
			fork := &types.Header{Number: big.NewInt(height), Difficulty: big.NewInt(2), ParentHash: parent}
			variants = append(variants, fork, h)
		}
		for _, candidate := range variants {
			fa.events <- transport.Notification{Result: testHeader(candidate)}
			fb.events <- transport.Notification{Result: testHeader(candidate)}
			if height == 100 {
				waitHead(t, s, func() bool {
					_, count := s.tracker.ArrivalSample(AnchorNode{Hash: candidate.Hash()})
					return count == 3
				})
			}
			e, err := s.Next(ctx)
			version++
			if err != nil || e.Path.NewVersion != version || e.Path.Appended[len(e.Path.Appended)-1].Node.Hash != candidate.Hash() {
				t.Fatalf("height %d: %+v %v", height, e, err)
			}
			s.mu.Lock()
			nodes, active := s.tracker.RetainedNodes(), s.activeRequests
			used := s.budget.Used()
			s.mu.Unlock()
			if used > peak {
				peak = used
			}
			if nodes > 7 || active != 0 || used > 64<<10 {
				t.Fatalf("real Header retained state grew: nodes=%d requests=%d bytes=%d", nodes, active, used)
			}
		}
		parent = h.Hash()
	}
	if err := s.Close(); err != nil || s.budget.Used() != 0 {
		t.Fatalf("long stream close: %v retained=%d", err, s.budget.Used())
	}
	t.Logf("real Header stream: %d deltas, retained peak=%d bytes after delivery", version, peak)
}

func TestHeadAuditClockExpiresOldestFactWithUnreadQueue(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	policy := testHeadPolicy()
	policy.AuditTimeout = 50 * time.Millisecond
	policy.Gate = HeadGatePolicy{Initial: time.Millisecond, Min: time.Millisecond, Max: time.Millisecond}
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, policy, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	base := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	fa.events <- transport.Notification{Result: testHeader(base)}
	fb.events <- transport.Notification{Result: testHeader(base)}
	waitHead(t, s, func() bool { return s.tracker.State().PathVersion == 1 })
	next := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: base.Hash()}
	fa.events <- transport.Notification{Result: testHeader(next)}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("periodic gate facts reset the oldest audit deadline")
	}
	ctx := context.Background()
	if e, err := s.Next(ctx); err != nil || e.Path.NewVersion != 1 {
		t.Fatalf("unread committed delta lost: %+v %v", e, err)
	}
	_, err = s.Next(ctx)
	var terminal *HeadTerminalError
	if !errors.As(err, &terminal) || !errors.Is(err, ErrHeadAuditTimeout) {
		t.Fatalf("audit terminal: %v", err)
	}
	gate := false
	for record := range terminal.Audit.All() {
		gate = gate || record.Reason == HeadGateExpired
	}
	if !gate {
		t.Fatal("waiting windows did not leave audit evidence")
	}
}

func TestHeadEstimatorUsesCommittedSameHashOutsideLockAndClamps(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	policy := testHeadPolicy()
	policy.Gate = HeadGatePolicy{Initial: 7 * time.Millisecond, Min: 5 * time.Millisecond, Max: 10 * time.Millisecond}
	var s *HeadSubscription
	samples := make(chan HeadCommittedSample, 2)
	policy.Gate.Estimate = func(sample HeadCommittedSample) time.Duration {
		_ = s.Basis() // Would deadlock if the callback ran under the core lock.
		samples <- sample
		if sample.Node.Number == 100 {
			return -time.Second
		}
		return time.Hour
	}
	var err error
	s, err = startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, policy, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var parent [32]byte
	for i, expected := range []time.Duration{5 * time.Millisecond, 10 * time.Millisecond} {
		h := &types.Header{Number: big.NewInt(int64(100 + i)), Difficulty: big.NewInt(1), ParentHash: parent}
		parent = h.Hash()
		fa.events <- transport.Notification{Result: testHeader(h)}
		waitHead(t, s, func() bool { return s.tracker.PendingAudit() > 0 })
		select {
		case <-samples:
			t.Fatal("single-source uncommitted observation reached estimator")
		default:
		}
		fb.events <- transport.Notification{Result: testHeader(h)}
		if _, err := s.Next(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case sample := <-samples:
			if sample.Node.Hash != parent || sample.Sources != 2 || sample.ArrivalSpread < 0 {
				t.Fatalf("invalid estimator sample %+v", sample)
			}
		case <-ctx.Done():
			t.Fatal("estimator blocked under a core lock")
		}
		waitHead(t, s, func() bool { return s.gateDuration == expected })
	}
}

func TestHeadCanceledObsoleteRequestOccupiesLimitUntilItExits(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	base := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	x101 := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: base.Hash()}
	x102 := &types.Header{Number: big.NewInt(102), Difficulty: big.NewInt(1), ParentHash: x101.Hash()}
	y101 := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(2), ParentHash: base.Hash()}
	y102 := &types.Header{Number: big.NewInt(102), Difficulty: big.NewInt(2), ParentHash: y101.Hash()}
	started, canceled, release, replacement := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	lookup := func(ctx context.Context, _ string, args ...any) (json.RawMessage, error) {
		if args[0] == x101.Hash().Hex() {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
			return nil, ctx.Err()
		}
		if args[0] == y101.Hash().Hex() {
			close(replacement)
			return testHeader(y101), nil
		}
		return nil, errors.New("unexpected hash")
	}
	policy := testHeadPolicy()
	policy.MaxConcurrentRequests = 1
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{&headLookupSource{a, lookup}, b}, policy, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		unblock()
		_ = s.Close() // Cleanup joins subscription work; Close always returns nil.
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, feed := range []*newHeadTestFeed{fa, fb} {
		feed.events <- transport.Notification{Result: testHeader(base)}
	}
	if _, err := s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	for _, feed := range []*newHeadTestFeed{fa, fb} {
		feed.events <- transport.Notification{Result: testHeader(x102)}
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("old fork recovery did not start")
	}
	for _, feed := range []*newHeadTestFeed{fa, fb} {
		feed.events <- transport.Notification{Result: testHeader(y102)}
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("proven useless fork request was not canceled")
	}
	s.mu.Lock()
	active := s.activeRequests
	s.mu.Unlock()
	if active != 1 {
		t.Fatalf("canceled but executing request occupies %d slots", active)
	}
	select {
	case <-replacement:
		t.Fatal("replacement exceeded concurrency while old request was executing")
	default:
	}
	unblock()
	e, err := s.Next(ctx)
	if err != nil || len(e.Path.Appended) != 2 || e.Path.Appended[1].Node.Hash != y102.Hash() {
		t.Fatalf("replacement did not progress after old request exit: %+v %v", e, err)
	}
}

func TestHeadUnreadReorgRoundTripKeepsEveryDelta(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	base := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	x := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: base.Hash()}
	y := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(2), ParentHash: base.Hash()}
	for index, h := range []*types.Header{base, x, y, x} {
		fa.events <- transport.Notification{Result: testHeader(h)}
		fb.events <- transport.Notification{Result: testHeader(h)}
		waitHead(t, s, func() bool { return s.tracker.State().PathVersion == uint64(index+1) })
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for index, h := range []*types.Header{base, x, y, x} {
		e, err := s.Next(ctx)
		if err != nil || e.Path.NewVersion != uint64(index+1) || e.Path.Appended[0].Node.Hash != h.Hash() {
			t.Fatalf("unread delta %d collapsed: %+v %v", index, e, err)
		}
		if index >= 2 && len(e.Path.Retracted) != 1 {
			t.Fatal("reorg lost its retraction")
		}
	}
}

func TestHeadCloseJoinsCancelledLookupBeforeReturning(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	started := make(chan struct{}, 2)
	cancelled := make(chan struct{}, 2)
	release := make(chan struct{})
	lookup := func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
		if m != "eth_getBlockByHash" {
			return nil, errors.New("no probe")
		}
		started <- struct{}{}
		<-ctx.Done()
		cancelled <- struct{}{}
		<-release
		return nil, ctx.Err()
	}
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{&headLookupSource{a, lookup}, &headLookupSource{b, lookup}}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	n100 := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	n102 := &types.Header{Number: big.NewInt(102), Difficulty: big.NewInt(1)}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	fa.events <- transport.Notification{Result: testHeader(n100)}
	fb.events <- transport.Notification{Result: testHeader(n100)}
	if _, err = s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	fa.events <- transport.Notification{Result: testHeader(n102)}
	fb.events <- transport.Notification{Result: testHeader(n102)}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("ancestor request did not start")
		}
	}
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-cancelled:
		case <-ctx.Done():
			t.Fatal("lookup not cancelled")
		}
	}
	select {
	case <-done:
		t.Fatal("Close did not join network tasks")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Close did not complete")
	}
	if s.budget.Used() != 0 {
		t.Fatalf("retained bytes %d", s.budget.Used())
	}
}

func TestHeadAncestorResponseCannotBeResignedAfterSessionChange(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	n100 := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	n101 := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: n100.Hash()}
	n102 := &types.Header{Number: big.NewInt(102), Difficulty: big.NewInt(1), ParentHash: n101.Hash()}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	lookup := func(ctx context.Context, m string, args ...any) (json.RawMessage, error) {
		if m != "eth_getBlockByHash" {
			return nil, errors.New("no probe")
		}
		started <- struct{}{}
		select {
		case <-release:
			return testHeader(n101), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	policy := testHeadPolicy()
	policy.AuditTimeout = 200 * time.Millisecond
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{&headLookupSource{a, lookup}, &headLookupSource{b, lookup}}, policy, rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	fa.events <- transport.Notification{Result: testHeader(n100)}
	fb.events <- transport.Notification{Result: testHeader(n100)}
	if _, err = s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	fa.events <- transport.Notification{Result: testHeader(n102)}
	fb.events <- transport.Notification{Result: testHeader(n102)}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("request did not start")
		}
	}
	// Simulate source reconnect while requests from the old physical sessions
	// remain in flight. These completions must keep their original identity.
	for _, id := range []string{"a", "b"} {
		session, err := s.sessions.Open(id)
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		err = s.tracker.Bind(session, time.Now())
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	if event, err := s.Next(ctx); !errors.Is(err, ErrRecoveryExhausted) && !errors.Is(err, ErrAuditTimeout) {
		t.Fatalf("old sessions entered ancestor quorum: %+v %v", event, err)
	}
}

func TestHeadAncestorQuorumDoesNotWaitForBlockedMinority(t *testing.T) {
	a, fa := newHeadSource("a")
	b, fb := newHeadSource("b")
	c, _ := newHeadSource("c")
	n100 := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
	n101 := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: n100.Hash()}
	n102 := &types.Header{Number: big.NewInt(102), Difficulty: big.NewInt(1), ParentHash: n101.Hash()}
	returned := make(chan struct{}, 2)
	slowStarted := make(chan struct{})
	slowCancelled := make(chan struct{})
	releaseSlow := make(chan struct{})
	markSlowStarted := sync.OnceFunc(func() { close(slowStarted) })
	markSlowCancelled := sync.OnceFunc(func() { close(slowCancelled) })
	unblockSlow := sync.OnceFunc(func() { close(releaseSlow) })
	fast := func() func(context.Context, string, ...any) (json.RawMessage, error) {
		markReturned := sync.OnceFunc(func() { returned <- struct{}{} })
		return func(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
			if method != "eth_getBlockByHash" || len(args) != 2 || args[0] != n101.Hash().Hex() || args[1] != false {
				return nil, errors.New("unexpected ancestor request")
			}
			// A later evaluation may legitimately fetch this object again.
			// Signal each supplier's first return without blocking repeats.
			markReturned()
			return testHeader(n101), nil
		}
	}
	slow := func(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
		if method != "eth_getBlockByHash" {
			return nil, errors.New("no height probe")
		}
		markSlowStarted()
		<-ctx.Done()
		markSlowCancelled()
		<-releaseSlow
		return nil, ctx.Err()
	}
	s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{
		&headLookupSource{a, fast()}, &headLookupSource{b, fast()}, &headLookupSource{c, slow},
	}, testHeadPolicy(), rpc.CorroborationMajority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblockSlow()
		_ = s.Close()
	})
	// Ensure the slow source belongs to the frozen fetch snapshot, even though
	// A/B can admit both business candidates without C's vote.
	deadline := time.Now().Add(time.Second)
	for {
		if _, err = s.sessions.Current("c"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow source session was not established")
		}
		time.Sleep(time.Millisecond)
	}
	fa.events <- transport.Notification{Result: testHeader(n100)}
	fb.events <- transport.Notification{Result: testHeader(n100)}
	initialCtx, cancelInitial := context.WithTimeout(context.Background(), time.Second)
	_, err = s.Next(initialCtx)
	cancelInitial()
	if err != nil {
		t.Fatal(err)
	}
	fa.events <- transport.Notification{Result: testHeader(n102)}
	fb.events <- transport.Notification{Result: testHeader(n102)}
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow ancestor request was not started")
	}
	for range 2 {
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("valid ancestor source did not return")
		}
	}
	type completion struct {
		event HeadEvent
		err   error
	}
	next := make(chan completion, 1)
	go func() { event, err := s.Next(context.Background()); next <- completion{event, err} }()
	progressed := false
	select {
	case got := <-next:
		progressed = got.err == nil && len(got.event.Path.Appended) == 2
		if !progressed {
			t.Errorf("unexpected ancestor completion %+v %v", got.event, got.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("A/B already supplied fixed ancestor quorum Q=2, but blocked C prevented path advancement with RecoveryTimeout=0")
	}
	// Regardless of early quorum success/failure, explicit Close must cancel
	// and join the outstanding minority task instead of abandoning it.
	closed := make(chan struct{})
	go func() { _ = s.Close(); close(closed) }()
	select {
	case <-slowCancelled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel minority ancestor request")
	}
	select {
	case <-closed:
		t.Error("Close returned before minority request exited")
	default:
	}
	unblockSlow()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close failed to join completed minority request")
	}
	if !progressed {
		select {
		case <-next:
		case <-time.After(time.Second):
			t.Fatal("pending Next remained blocked after Close")
		}
	}
}
