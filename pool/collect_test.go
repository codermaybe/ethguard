package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	upstream "github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// --- Fake dialer + fake RPC node --------------------------------------------

// fakeNode serves an in-process JSON-RPC server speaking a minimal namespace.
type fakeNode struct {
	blockNum    atomic.Int64
	blockOffset atomic.Int64
	tracker     *callTracker
}

type callTracker struct {
	active    atomic.Int64
	maxActive atomic.Int64
	delay     time.Duration
	entered   chan<- struct{}
	release   <-chan struct{}
}

func (t *callTracker) enter() func() {
	active := t.active.Add(1)
	for {
		maxActive := t.maxActive.Load()
		if active <= maxActive || t.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	if t.entered != nil {
		t.entered <- struct{}{}
	}
	if t.release != nil {
		<-t.release
	} else {
		time.Sleep(t.delay)
	}
	return func() { t.active.Add(-1) }
}

// fakeAPI exposes the node's RPC methods.
type fakeAPI struct {
	n *fakeNode
}

func (a *fakeAPI) ChainId() (interface{}, error) { return "0x1", nil }

type fakeRPCError struct {
	code int
}

func (e fakeRPCError) Error() string  { return "protocol error" }
func (e fakeRPCError) ErrorCode() int { return e.code }

// BlockNumber returns a value for the eth_blockNumber method (per-node counter).
func (a *fakeAPI) BlockNumber() (interface{}, error) {
	if a.n.tracker != nil {
		defer a.n.tracker.enter()()
	}
	return a.n.blockNum.Add(1) + a.n.blockOffset.Load(), nil
}

func (a *fakeAPI) Fail() (interface{}, error) {
	return nil, errors.New("execution reverted")
}

func (a *fakeAPI) ProtocolFailure() (interface{}, error) {
	return nil, fakeRPCError{code: -32700}
}

func (a *fakeAPI) ServerFailure() (interface{}, error) {
	return nil, fakeRPCError{code: -32603}
}

// newFakeDialer returns a dialer that spins up an in-memory RPC node per URL,
// so each Source gets its own independent value. It counts dials per URL.
type newFakeDialer struct {
	mu          sync.Mutex
	dials       map[string]int
	fails       map[string]error
	closeFirst  map[string]bool
	dialDelay   time.Duration
	track       *callTracker
	clients     map[string][]*ethclient.Client
	dialEntered chan<- struct{}
	dialRelease <-chan struct{}
	offsets     map[string]int64
	nodes       map[string]*fakeNode
}

// setOffset moves a node's block height, including an already-dialed node.
func (d *newFakeDialer) setOffset(rawURL string, off int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.offsets[rawURL] = off
	if n := d.nodes[rawURL]; n != nil {
		n.blockOffset.Store(off)
	}
}

func (d *newFakeDialer) DialContext(ctx context.Context, rawURL string) (Transport, error) {
	d.mu.Lock()
	d.dials[rawURL]++
	dialNumber := d.dials[rawURL]
	err := d.fails[rawURL]
	closeFirst := d.closeFirst[rawURL]
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if d.dialDelay > 0 {
		time.Sleep(d.dialDelay)
	}
	if d.dialEntered != nil {
		d.dialEntered <- struct{}{}
	}
	if d.dialRelease != nil {
		select {
		case <-d.dialRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	n := &fakeNode{tracker: d.track}
	if d.offsets != nil {
		n.blockOffset.Store(d.offsets[rawURL])
	}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", &fakeAPI{n: n}); err != nil {
		return nil, err
	}
	if d.nodes != nil {
		d.mu.Lock()
		d.nodes[rawURL] = n
		d.mu.Unlock()
	}

	client := ethclient.NewClient(rpc.DialInProc(server))
	if closeFirst && dialNumber == 1 {
		client.Close()
	}
	d.mu.Lock()
	if d.clients != nil {
		d.clients[rawURL] = append(d.clients[rawURL], client)
	}
	d.mu.Unlock()
	return upstream.NewGethTransport(client), nil
}

// --- Tests -------------------------------------------------------------------

func TestCollectFanoutReturnsOneEmissionPerSource(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)

	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Join("b", "https://b.example"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ems, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ems) != 2 {
		t.Fatalf("expected 2 emissions, got %d", len(ems))
	}
	for i, em := range ems {
		if em.Err != nil {
			t.Fatalf("emission %d: unexpected err %v", i, em.Err)
		}
		if len(em.Val) == 0 {
			t.Fatalf("emission %d: no value", i)
		}
	}
}

func TestCollectRejectsUnexpectedChainID(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 5)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrChainIDMismatch) {
		t.Fatalf("expected chain ID mismatch, got %v", err)
	}
	if src.eligible(time.Now()) {
		t.Fatal("chain-mismatched source must remain ineligible")
	}
}

func TestCollectDialOncePerSource(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if got := d.dials["https://a.example"]; got != 1 {
		t.Fatalf("expected single dial, got %d", got)
	}
}

func TestCollectDeadlineIncludesWaitForSharedDial(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	d := &newFakeDialer{
		dials:       make(map[string]int),
		dialEntered: entered,
		dialRelease: release,
	}
	p := newPoolWithDialerAndChainID(d, 0)
	defer p.Close()
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
		firstDone <- err
	}()
	<-entered

	type collectResult struct {
		emissions []Emission
		err       error
	}
	secondDone := make(chan collectResult, 1)
	go func() {
		emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{
			Timeout: 20 * time.Millisecond,
		})
		secondDone <- collectResult{emissions: emissions, err: err}
	}()

	var second collectResult
	select {
	case second = <-secondDone:
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-firstDone
		<-secondDone
		t.Fatal("Collect remained blocked waiting for another call's dial")
	}
	if second.err == nil {
		t.Fatal("expected the short collection to time out")
	}
	if len(second.emissions) != 1 {
		t.Fatalf("expected one emission, got %+v", second.emissions)
	}
	// The waiter gave up either on its own deadline or while waiting for the
	// shared dial; both must surface the deadline cause, not a dial failure.
	if !errors.Is(second.emissions[0].Err, context.DeadlineExceeded) &&
		!errors.Is(second.emissions[0].Err, errWaitingForDial) {
		t.Fatalf("expected a deadline or waiting emission, got %+v", second.emissions)
	}
	if got := src.Health(); got.Failures != 0 {
		t.Fatalf("waiting for a shared dial should not count as a source failure: %+v", got)
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if got := d.dials["https://a.example"]; got != 1 {
		t.Fatalf("expected one shared dial, got %d", got)
	}
}

func TestCollectRespectsMaxConcurrent(t *testing.T) {
	tracker := &callTracker{delay: 25 * time.Millisecond}
	d := &newFakeDialer{
		dials: make(map[string]int),
		track: tracker,
	}
	p := newPoolWithDialerAndChainID(d, 0)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}

	ems, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{
		MaxConcurrent: 2,
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ems) != 6 {
		t.Fatalf("expected 6 emissions, got %d", len(ems))
	}
	if got := tracker.maxActive.Load(); got != 2 {
		t.Fatalf("expected at most 2 concurrent calls, got %d", got)
	}
}

func TestCollectRecordsPerSourceHealthWithoutBlockingSuccess(t *testing.T) {
	dialErr := errors.New("dial failed")
	d := &newFakeDialer{
		dials: make(map[string]int),
		fails: map[string]error{"https://b.example": dialErr},
	}
	p := newPoolWithDialerAndChainID(d, 0)
	a, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Join("b", "https://b.example")
	if err != nil {
		t.Fatal(err)
	}

	ems, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ems) != 2 {
		t.Fatalf("expected 2 emissions, got %d", len(ems))
	}
	if ems[0].Src != a || ems[0].Err != nil || len(ems[0].Val) == 0 {
		t.Fatalf("unexpected successful emission: %+v", ems[0])
	}
	if ems[1].Src != b || !errors.Is(ems[1].Err, dialErr) || ems[1].Val != nil {
		t.Fatalf("unexpected failed emission: %+v", ems[1])
	}

	if got := a.Health(); got.Successes != 1 || got.Failures != 0 {
		t.Fatalf("unexpected healthy source stats: %+v", got)
	}
	if got := b.Health(); got.Successes != 0 || got.Failures != 1 || !errors.Is(got.LastError, dialErr) {
		t.Fatalf("unexpected failed source stats: %+v", got)
	}
	if a.State() != SourceReady {
		t.Fatalf("expected successful source ready, got %v", a.State())
	}
	if b.State() != SourceDown {
		t.Fatalf("expected failed source down, got %v", b.State())
	}
}

func TestCollectTreatsJSONRPCErrorAsAHealthyResponse(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}

	ems, err := p.Collect(context.Background(), "eth_fail", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("expected valid JSON-RPC error to count as a response: %v", err)
	}
	if len(ems) != 1 || ems[0].Err == nil {
		t.Fatalf("expected the JSON-RPC error in its emission, got %+v", ems)
	}
	if got := src.Health(); got.Successes != 1 || got.Failures != 0 {
		t.Fatalf("expected a healthy response observation, got %+v", got)
	}
	if src.State() != SourceReady {
		t.Fatalf("expected source ready after a valid JSON-RPC response, got %v", src.State())
	}
}

func TestCollectRecordsJSONRPCProtocolErrorAsFailure(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}

	ems, err := p.Collect(context.Background(), "eth_protocolFailure", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("expected protocol error emission to be returned: %v", err)
	}
	if len(ems) != 1 || ems[0].Err == nil {
		t.Fatalf("expected protocol error emission, got %+v", ems)
	}
	if got := src.Health(); got.Successes != 0 || got.Failures != 1 {
		t.Fatalf("expected protocol failure observation, got %+v", got)
	}
	if src.State() != SourceDown {
		t.Fatalf("expected source down after protocol failure, got %v", src.State())
	}
}

func TestCollectTemporarilyExcludesRepeatedlyFailingSource(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < eligibleFailureThreshold; i++ {
		if _, err := p.Collect(context.Background(), "eth_protocolFailure", nil, CollectOptions{}); err != nil {
			t.Fatalf("failure %d: %v", i+1, err)
		}
	}
	if got := src.Health().ConsecutiveFailures; got != eligibleFailureThreshold {
		t.Fatalf("expected %d consecutive failures, got %d", eligibleFailureThreshold, got)
	}
	emissions, err := p.Collect(context.Background(), "eth_protocolFailure", nil, CollectOptions{})
	if !errors.Is(err, ErrNoEligibleSource) {
		t.Fatalf("expected no-eligible-source class, got %v", err)
	}
	if len(emissions) != 0 {
		t.Fatalf("expected no emissions from an open circuit, got %+v", emissions)
	}
	if got := d.dials["https://a.example"]; got != 1 {
		t.Fatalf("open circuit should avoid another dial, got %d", got)
	}
}

func TestCollectDoesNotBlameSourceForCallerDeadline(t *testing.T) {
	tracker := &callTracker{delay: 100 * time.Millisecond}
	d := &newFakeDialer{
		dials: make(map[string]int),
		track: tracker,
	}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{Timeout: time.Second}); err == nil {
		t.Fatal("expected caller deadline")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected caller deadline to be preserved, got %v", err)
	}
	if got := src.Health(); got.Successes != 0 || got.Failures != 0 {
		t.Fatalf("expected no source observation for caller deadline, got %+v", got)
	}
}

func TestCollectDoesNotBlameSourceForCallerCancellation(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err == nil {
		t.Fatal("expected canceled collection")
	}
	if got := src.Health(); got.Successes != 0 || got.Failures != 0 {
		t.Fatalf("expected no source observation for caller cancellation, got %+v", got)
	}
}

func TestCollectDoesNotBlameSourceThatNeverAcquiredConcurrencySlot(t *testing.T) {
	tracker := &callTracker{delay: 100 * time.Millisecond}
	d := &newFakeDialer{
		dials: make(map[string]int),
		track: tracker,
	}
	p := newPoolWithDialerAndChainID(d, 0)
	for _, name := range []string{"a", "b"} {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{
		Timeout:       10 * time.Millisecond,
		MaxConcurrent: 1,
	}); err == nil {
		t.Fatal("expected collection timeout")
	}

	var failures uint64
	for _, src := range p.Sources() {
		failures += src.Health().Failures
	}
	if failures != 1 {
		t.Fatalf("expected only the attempted source to fail, got %d failures", failures)
	}
}

func TestCollectLatencyExcludesConcurrencyQueueTime(t *testing.T) {
	tracker := &callTracker{delay: 20 * time.Millisecond}
	d := &newFakeDialer{
		dials: make(map[string]int),
		track: tracker,
	}
	p := newPoolWithDialerAndChainID(d, 0)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{
		MaxConcurrent: 1,
	}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var minRTT, maxRTT time.Duration
	for i, src := range p.Sources() {
		rtt := src.Health().LatencyEWMA
		if i == 0 || rtt < minRTT {
			minRTT = rtt
		}
		if rtt > maxRTT {
			maxRTT = rtt
		}
	}
	if maxRTT > minRTT*3 {
		t.Fatalf("latency appears to include queue time: min=%v max=%v", minRTT, maxRTT)
	}
}

func TestCollectLatencyExcludesLazyDialOnFirstSample(t *testing.T) {
	// The first call through a source pays the lazy dial and the first-use
	// chain-ID verification before its CallContext; neither may inflate the
	// RTT sample that health percentiles keep. A 100ms dial must not appear
	// in the first sample of either fan-out path.
	const (
		dialDelay    = 100 * time.Millisecond
		rttAllowance = 50 * time.Millisecond
	)
	d := &newFakeDialer{dials: make(map[string]int), dialDelay: dialDelay}
	p := newPoolWithDialerAndChainID(d, 1) // first call also pays eth_chainId
	for _, name := range []string{"a", "b"} {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}

	ems, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{
		Sources: p.Sources()[:1],
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ems) != 1 || ems[0].Err != nil || ems[0].Val == nil {
		t.Fatalf("unexpected first emission: %+v", ems)
	}
	if got := ems[0].RTT; got > rttAllowance {
		t.Fatalf("first Collect sample includes lazy dial: RTT=%v", got)
	}
	if got := p.Sources()[0].Health().LatencyEWMA; got > rttAllowance {
		t.Fatalf("first health sample includes lazy dial: EWMA=%v", got)
	}

	forwardEms, err := p.Forward(context.Background(), "eth_blockNumber", nil, CollectOptions{
		Sources: p.Sources()[1:],
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if len(forwardEms) == 0 || forwardEms[0].Err != nil || forwardEms[0].Val == nil {
		t.Fatalf("unexpected Forward emission: %+v", forwardEms)
	}
	if got := forwardEms[0].RTT; got > rttAllowance {
		t.Fatalf("first Forward sample includes lazy dial: RTT=%v", got)
	}
	if got := d.dials["https://a.example"]; got != 1 {
		t.Fatalf("expected the lazy dial to actually happen, got %d dials", got)
	}
}

func TestPoolCloseReleasesConnectionsAndRejectsFurtherUse(t *testing.T) {
	d := &newFakeDialer{
		dials:   make(map[string]int),
		clients: make(map[string][]*ethclient.Client),
	}
	p := newPoolWithDialerAndChainID(d, 0)
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	p.Close()
	p.Close() // idempotent

	var result interface{}
	client := d.clients["https://a.example"][0]
	if err := client.Client().CallContext(context.Background(), &result, "eth_blockNumber"); !errors.Is(err, rpc.ErrClientQuit) {
		t.Fatalf("expected underlying client closed, got %v", err)
	}
	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected closed pool error, got %v", err)
	}
	if _, err := p.Join("b", "https://b.example"); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected Join on closed pool to fail, got %v", err)
	}
}

func TestPoolCloseDoesNotBlameOrReviveInFlightSource(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	d := &newFakeDialer{
		dials: make(map[string]int),
		track: &callTracker{entered: entered, release: release},
	}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
		done <- err
	}()
	<-entered

	p.Close()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expected in-flight collection to stop when pool closes")
	}
	if src.State() != SourceDown {
		t.Fatalf("expected closed source down, got %v", src.State())
	}
	if got := src.Health(); got.Successes != 0 || got.Failures != 0 {
		t.Fatalf("expected Close not to affect source health, got %+v", got)
	}
}

func TestPoolCloseCancelsInFlightDial(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	d := &newFakeDialer{
		dials:       make(map[string]int),
		dialEntered: entered,
		dialRelease: release,
	}
	p := newPoolWithDialerAndChainID(d, 0)
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	collectDone := make(chan error, 1)
	go func() {
		_, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
		collectDone <- err
	}()
	<-entered

	closeDone := make(chan struct{})
	go func() {
		p.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
		close(release)
	case <-time.After(50 * time.Millisecond):
		close(release)
		<-closeDone
		t.Fatal("Close blocked behind in-flight dial")
	}
	if err := <-collectDone; err == nil {
		t.Fatal("expected canceled dial to fail collection")
	}
}

func TestCollectRedialsAfterUnrecoverableClientFailure(t *testing.T) {
	d := &newFakeDialer{
		dials:      make(map[string]int),
		closeFirst: map[string]bool{"https://a.example": true},
	}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); err == nil {
		t.Fatal("expected first call through closed client to fail")
	}
	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatalf("expected second call to redial successfully: %v", err)
	}
	if got := d.dials["https://a.example"]; got != 2 {
		t.Fatalf("expected two dials, got %d", got)
	}
	if src.State() != SourceReady {
		t.Fatalf("expected recovered source ready, got %v", src.State())
	}
}

func TestPoolJoinIsAtomicAndReturnsCanonicalSource(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	const goroutines = 32
	results := make(chan *Source, goroutines)
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src, err := p.Join("a", "https://a.example")
			results <- src
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
	}
	var canonical *Source
	for src := range results {
		if canonical == nil {
			canonical = src
		}
		if src != canonical {
			t.Fatal("concurrent Join returned a non-canonical source")
		}
	}
	if p.Len() != 1 {
		t.Fatalf("expected one source, got %d", p.Len())
	}
}

func TestPoolJoinDeduplicatesNormalizedEndpoints(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	first, err := p.Join("first", "HTTPS://EXAMPLE.COM/")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Join("second", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || p.Len() != 1 {
		t.Fatalf("expected one canonical source, got first=%p second=%p len=%d", first, second, p.Len())
	}
}

func TestPoolJoinRejectsDuplicateDisplayNames(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	if _, err := p.Join("same", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Join("same", "https://b.example"); err == nil {
		t.Fatal("expected duplicate display name to be rejected")
	}
}

func TestPoolJoinRejectsUnsupportedScheme(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	if _, err := p.Join("bad", "ftp://example.com"); err == nil {
		t.Fatal("expected unsupported scheme to be rejected")
	}
}

func TestCollectVerdictIsIndependentOfMemberOrder(t *testing.T) {
	// Adjudication must not depend on which member happens to sit first in
	// the slice: a usable answer outranks the identity sentinel in both
	// arrangements. Previously the sentinel won whenever it came first, so
	// the same fleet answered differently after a re-order.
	mismatched := rpctest.NewNode(1) // pool below expects chain 5
	defer mismatched.Close()
	mismatched.Return("eth_blockNumber", "0x10")
	honest := rpctest.NewNode(5)
	defer honest.Close()
	honest.Return("eth_blockNumber", "0x11")

	for _, tc := range []struct {
		name   string
		first  *rpctest.Node
		second *rpctest.Node
	}{
		{"sentinel first", mismatched, honest},
		{"sentinel last", honest, mismatched},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPoolWithChainID(5)
			defer p.Close()
			if _, err := p.Join("a", tc.first.URL()); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Join("b", tc.second.URL()); err != nil {
				t.Fatal(err)
			}
			emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatalf("usable answer must outrank the identity sentinel: %v", err)
			}
			usable, mismatchedSentinel := 0, 0
			for _, em := range emissions {
				if em.Val != nil {
					usable++
				}
				if errors.Is(em.Err, ErrChainIDMismatch) {
					mismatchedSentinel++
				}
			}
			if usable != 1 || mismatchedSentinel != 1 {
				t.Fatalf("want one usable answer and one identity sentinel, got %d/%d in %+v",
					usable, mismatchedSentinel, emissions)
			}
		})
	}
}

func TestCollectUsableAnswerOutranksFanoutTimeout(t *testing.T) {
	// A member that answered before the fan-out deadline must not be
	// discarded by the timeout verdict: the usable answer wins and the
	// laggard keeps its timeout on its own emission.
	fast := rpctest.NewNode(1)
	defer fast.Close()
	fast.Handle("eth_blockNumber", rpctest.After(30*time.Millisecond, rpctest.Value("0x11")))
	laggard := rpctest.NewNode(1)
	defer laggard.Close()
	laggard.Hang("eth_blockNumber", time.Second)

	p := NewPoolWithChainID(1)
	defer p.Close()
	if _, err := p.Join("fast", fast.URL()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Join("laggard", laggard.URL()); err != nil {
		t.Fatal(err)
	}
	emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{Timeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("answered member must outrank the fan-out timeout: %v", err)
	}
	usable, timedOut := 0, 0
	for _, em := range emissions {
		if em.Val != nil {
			usable++
		}
		if errors.Is(em.Err, context.DeadlineExceeded) {
			timedOut++
		}
	}
	if usable != 1 || timedOut != 1 {
		t.Fatalf("want one answer and one per-source timeout, got %d/%d in %+v", usable, timedOut, emissions)
	}
}

func TestCollectClassifiesEmptyPoolAsNoEligibleSource(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if !errors.Is(err, ErrNoEligibleSource) {
		t.Fatalf("expected no-eligible-source class, got %v", err)
	}
	if len(emissions) != 0 {
		t.Fatalf("expected no emissions without sources, got %+v", emissions)
	}
}

func TestCollectClassifiesDeadFleetAsAllSourcesDown(t *testing.T) {
	dialErr := errors.New("dial failed")
	d := &newFakeDialer{
		dials: make(map[string]int),
		fails: map[string]error{
			"https://a.example": dialErr,
			"https://b.example": dialErr,
		},
	}
	p := newPoolWithDialerAndChainID(d, 0)
	for _, name := range []string{"a", "b"} {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}

	emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if !errors.Is(err, ErrAllSourcesDown) {
		t.Fatalf("expected all-sources-down class, got %v", err)
	}
	if len(emissions) != 2 {
		t.Fatalf("expected per-source emissions preserved, got %+v", emissions)
	}
	for i, em := range emissions {
		if !errors.Is(em.Err, dialErr) {
			t.Fatalf("emission %d: expected original cause preserved, got %v", i, em.Err)
		}
	}
}

func TestForwardFailsOverTransportErrorsInPoolOrder(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int), closeFirst: map[string]bool{"https://a.example": true}}
	p := newPoolWithDialerAndChainID(d, 0)
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Join("b", "https://b.example"); err != nil {
		t.Fatal(err)
	}
	emissions, err := p.Forward(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if len(emissions) != 2 || emissions[0].Err == nil || emissions[1].Err != nil || len(emissions[1].Val) == 0 {
		t.Fatalf("unexpected failover emissions: %+v", emissions)
	}
	if d.dials["https://a.example"] != 1 || d.dials["https://b.example"] != 1 {
		t.Fatalf("expected one dial per attempted source, got %+v", d.dials)
	}
}

func TestServerErrorsHaveDedicatedHealthBucket(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Collect(context.Background(), "eth_serverFailure", nil, CollectOptions{}); err != nil {
		t.Fatalf("expected protocol response to be returned: %v", err)
	}
	health := src.Health()
	if health.Failures != 1 || health.ProtocolServerErrors != 1 || health.Successes != 0 {
		t.Fatalf("unexpected server-error health: %+v", health)
	}
}
