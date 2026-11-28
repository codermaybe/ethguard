package pool

import (
	"context"
	"errors"
	"math/big"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/endpoint"
	upstream "github.com/codermaybe/ethguard/internal/transport"
)

type declaredSubscriptionDialer struct {
	contractDialer
	enabled atomic.Bool
	probes  atomic.Int32
}

func (d *declaredSubscriptionDialer) SupportsBoundedSubscriptions() bool {
	d.probes.Add(1)
	return d.enabled.Load()
}

func newDeclaredSubscriptionDialer(tr upstream.Transport, enabled bool) *declaredSubscriptionDialer {
	d := &declaredSubscriptionDialer{contractDialer: contractDialer{transport: tr}}
	d.enabled.Store(enabled)
	return d
}

type capabilitySubscription struct {
	events chan upstream.Notification
	err    chan error
	once   sync.Once
}

func (s *capabilitySubscription) Events() <-chan upstream.Notification { return s.events }
func (s *capabilitySubscription) Err() <-chan error                    { return s.err }
func (s *capabilitySubscription) Unsubscribe() {
	s.once.Do(func() { close(s.events); close(s.err) })
}

type boundedCapabilityTransport struct {
	contractTransport
	subscriptions atomic.Int32
}

func (tr *boundedCapabilityTransport) SubscribeBounded(context.Context, upstream.NotificationPolicy, string, ...any) (upstream.Subscription, error) {
	tr.subscriptions.Add(1)
	return &capabilitySubscription{events: make(chan upstream.Notification), err: make(chan error)}, nil
}

func TestSubscriptionMembershipUsesFrozenDialerCapabilityInsteadOfScheme(t *testing.T) {
	tr := &boundedCapabilityTransport{}
	tr.chainID = big.NewInt(1)
	enabled := newDeclaredSubscriptionDialer(tr, true)
	disabled := newDeclaredSubscriptionDialer(tr, false)
	plain := &contractDialer{transport: tr} // Actual connection supports it, but its factory declares nothing.
	pipe, err := endpoint.Parse(`\\.\pipe\geth.ipc`)
	if err != nil {
		t.Fatal(err)
	}
	members := []Member{
		{Name: "enabled-http", URL: "https://declared.example", Dialer: enabled},
		{Name: "disabled-wss", URL: "wss://disabled.example", Dialer: disabled},
		{Name: "undeclared-wss", URL: "wss://undeclared.example", Dialer: plain},
		{Name: "enabled-pipe", Endpoint: pipe, Dialer: enabled},
		{Name: "default-http", URL: "http://default.example"},
	}
	p, err := NewWithMembers(1, members, WithDialer(enabled))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if enabled.dialCount() != 0 || disabled.dialCount() != 0 || plain.dialCount() != 0 {
		t.Fatal("capability discovery dialed a connection")
	}
	want := []string{"enabled-http", "enabled-pipe", "default-http"}
	assertMembers := func() []*Source {
		t.Helper()
		sources, err := p.SubscriptionSources()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, source := range sources {
			got = append(got, source.Name())
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("subscription basis=%v, want %v", got, want)
		}
		if p.Len() != len(members) {
			t.Fatal("subscription filtering changed the unary voting basis")
		}
		return sources
	}
	first := assertMembers()
	first[0] = nil // Returned snapshots must not mutate registered membership.
	probeCount := enabled.probes.Load() + disabled.probes.Load()
	enabled.enabled.Store(false)
	disabled.enabled.Store(true)
	members[0].Dialer = disabled // Mutating caller-owned composition also cannot alter it.
	sources := assertMembers()
	if enabled.probes.Load()+disabled.probes.Load() != probeCount {
		t.Fatal("membership re-probed mutable dialer state")
	}
	stream, err := sources[0].SubscribeBounded(context.Background(), upstream.NotificationPolicy{Messages: 1, Bytes: 1024, MaxMessageBytes: 1024}, "newHeads")
	if err != nil {
		t.Fatalf("declared subscription at an HTTP-shaped address failed: %v", err)
	}
	stream.Unsubscribe()
	if enabled.dialCount() != 1 || tr.subscriptions.Load() != 1 {
		t.Fatal("declared source did not use its selected transport")
	}
	tr.mu.Lock()
	tr.err = errors.New("provider unavailable")
	tr.mu.Unlock()
	if _, err := sources[0].CallContext(context.Background(), "eth_blockNumber"); err == nil {
		t.Fatal("failure fixture unexpectedly succeeded")
	}
	if sources[0].Health().ConsecutiveFailures == 0 {
		t.Fatal("fixture did not affect source health")
	}
	assertMembers()
}

func TestUndeclaredWebSocketAddressDoesNotCreateSubscriptionCapability(t *testing.T) {
	dialer := newDeclaredSubscriptionDialer(&contractTransport{}, false)
	p, err := NewWithMembers(0, []Member{{Name: "looks-like-ws", URL: "ws://node.example", Dialer: dialer}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if sources, err := p.SubscriptionSources(); len(sources) != 0 || !errors.Is(err, ErrNoSubscriptionSource) {
		t.Fatalf("undeclared factory contributed subscriptions: sources=%v err=%v", sources, err)
	}
	if dialer.dialCount() != 0 {
		t.Fatal("absence discovery performed network I/O")
	}
}

func TestDeclaredSubscriptionCapabilityStillChecksDialedTransport(t *testing.T) {
	dialer := newDeclaredSubscriptionDialer(&contractTransport{}, true)
	p, err := NewWithMembers(0, []Member{{Name: "incorrect-declaration", URL: "http://node.example", Dialer: dialer}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	sources, err := p.SubscriptionSources()
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	stream, err := sources[0].SubscribeBounded(context.Background(), upstream.NotificationPolicy{Messages: 1, Bytes: 1024, MaxMessageBytes: 1024}, "newHeads")
	if stream != nil {
		stream.Unsubscribe()
	}
	if err == nil || stream != nil {
		t.Fatal("unary-only transport fulfilled a false bounded-subscription declaration")
	}
	if dialer.dialCount() != 1 {
		t.Fatalf("dials=%d, want one deferred check", dialer.dialCount())
	}
}

type frozenTargetDialer struct {
	contractDialer
	mu     sync.Mutex
	target string
}

func (d *frozenTargetDialer) DialContext(ctx context.Context, target string) (upstream.Transport, error) {
	d.mu.Lock()
	d.target = target
	d.mu.Unlock()
	return d.contractDialer.DialContext(ctx, target)
}

func TestParsedIPCMemberKeepsItsDialTargetAfterWorkingDirectoryChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix relative socket paths")
	}
	base, later := t.TempDir(), t.TempDir()
	t.Chdir(base)
	input := config.Config{Upstreams: []config.Upstream{{Endpoint: "./nested/../geth #?% pipe.ipc "}}}
	normalized, err := config.Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := endpoint.Parse(normalized.Upstreams[0].Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "geth #?% pipe.ipc ")
	wantIdentity := (&url.URL{Scheme: "ipc", Path: want}).String()
	if parsed.DialTarget() != want || parsed.CanonicalID() != wantIdentity {
		t.Fatalf("parsed=%+v, want frozen %q", parsed, want)
	}
	tr := &contractTransport{result: []byte(`"0x1"`)}
	dialer := &frozenTargetDialer{contractDialer: contractDialer{transport: tr}}
	p, err := NewWithMembers(0, []Member{{Name: normalized.Upstreams[0].ID, Endpoint: parsed, Dialer: dialer}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if dialer.dialCount() != 0 {
		t.Fatal("IPC configuration dialed eagerly")
	}
	t.Chdir(later)
	source := p.Sources()[0]
	if source.ID() != parsed.CanonicalID() || source.URL() != want {
		t.Fatalf("source identity=%q target=%q", source.ID(), source.URL())
	}
	if _, err := source.CallContext(context.Background(), "eth_blockNumber"); err != nil {
		t.Fatal(err)
	}
	dialer.mu.Lock()
	actual := dialer.target
	dialer.mu.Unlock()
	if actual != want || dialer.dialCount() != 1 {
		t.Fatalf("deferred target=%q dials=%d, want %q once", actual, dialer.dialCount(), want)
	}
}
