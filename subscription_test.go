package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalsubscription "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/typed"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeSubscription struct {
	events chan transport.Notification
	errs   chan error
}

type aggregateTestSource struct {
	id  string
	sub transport.Subscription
}

func (s *aggregateTestSource) ID() string { return s.id }
func (s *aggregateTestSource) Subscribe(context.Context, string, ...any) (transport.Subscription, error) {
	return s.sub, nil
}

type aggregateTestSubscription struct {
	events chan transport.Notification
	errs   chan error
}

type reconnectTestSource struct {
	id    string
	mu    sync.Mutex
	count int
	subs  []transport.Subscription
}
type blockingReconnectSource struct {
	id            string
	first         transport.Subscription
	replacement   transport.Subscription
	secondStarted chan struct{}
	releaseSecond chan struct{}
	mu            sync.Mutex
	count         int
}

type trackedSubscription struct {
	events       chan transport.Notification
	errs         chan error
	unsubscribed chan struct{}
	once         sync.Once
}

type pullTestSource struct {
	id      string
	stream  transport.Subscription
	pullRaw json.RawMessage
	pull    func(string, ...any) (json.RawMessage, error)
	pulls   atomic.Int32
}

func (s *pullTestSource) ID() string { return s.id }
func (s *pullTestSource) Subscribe(context.Context, string, ...any) (transport.Subscription, error) {
	return s.stream, nil
}
func (s *pullTestSource) CallContext(_ context.Context, method string, args ...any) (json.RawMessage, error) {
	s.pulls.Add(1)
	if s.pull != nil {
		return s.pull(method, args...)
	}
	return s.pullRaw, nil
}
func (s *pullTestSource) pullCount() int { return int(s.pulls.Load()) }

func (s *reconnectTestSource) ID() string { return s.id }
func (s *reconnectTestSource) Subscribe(context.Context, string, ...any) (transport.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count >= len(s.subs) {
		return nil, errors.New("no scripted subscription")
	}
	sub := s.subs[s.count]
	s.count++
	return sub, nil
}
func (s *reconnectTestSource) subscriptions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
func (s *blockingReconnectSource) ID() string { return s.id }
func (s *blockingReconnectSource) Subscribe(ctx context.Context, _ string, _ ...any) (transport.Subscription, error) {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 1 {
		return s.first, nil
	}
	if count == 2 {
		close(s.secondStarted)
		<-s.releaseSecond
		return s.replacement, nil
	}
	return nil, errors.New("unexpected subscription attempt")
}

func (s *trackedSubscription) Events() <-chan transport.Notification { return s.events }
func (s *trackedSubscription) Err() <-chan error                     { return s.errs }
func (s *trackedSubscription) Unsubscribe() {
	s.once.Do(func() { close(s.unsubscribed) })
}

func (s *aggregateTestSubscription) Events() <-chan transport.Notification { return s.events }
func (s *aggregateTestSubscription) Err() <-chan error                     { return s.errs }
func (s *aggregateTestSubscription) Unsubscribe() {
	// The consumer owns neither channel. Closing either one while a test is
	// injecting a notification would model an invalid transport and race with
	// the producer; aggregateEthSubscription.stop handles consumer shutdown.
}

func (s *fakeSubscription) Events() <-chan transport.Notification { return s.events }
func (s *fakeSubscription) Err() <-chan error                     { return s.errs }
func (s *fakeSubscription) Unsubscribe() {
	select {
	case <-s.events:
	default:
	}
}

func TestSubscriptionFilterArgMatchesGethShape(t *testing.T) {
	arg, err := subscriptionFilterArg(ethereum.FilterQuery{Addresses: []common.Address{{1}}, Topics: [][]common.Hash{{{2}}}})
	if err != nil {
		t.Fatalf("filter arg: %v", err)
	}
	if _, ok := arg["fromBlock"]; !ok {
		t.Fatal("missing default fromBlock")
	}
	if _, ok := arg["toBlock"]; !ok {
		t.Fatal("missing default toBlock")
	}
	if _, err := subscriptionFilterArg(ethereum.FilterQuery{BlockHash: ptrHash(common.Hash{1}), FromBlock: big.NewInt(1)}); err == nil {
		t.Fatal("accepted block hash with range")
	}
}

func TestValidateSubscriptionLogIdentityRequiresReorgBinding(t *testing.T) {
	if err := validateSubscriptionLogIdentity([]byte(`{"address":"0x0000000000000000000000000000000000000001"}`)); err == nil {
		t.Fatal("accepted log without block/transaction/index identity")
	}
	if err := validateSubscriptionLogIdentity([]byte(`{"blockHash":"0x1","transactionHash":"0x2","logIndex":"0x0"}`)); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
}

func TestStartSubscriptionDecodesAndPropagatesErrors(t *testing.T) {
	inner := &fakeSubscription{events: make(chan transport.Notification, 1), errs: make(chan error, 1)}
	out := make(chan *struct {
		Number string `json:"number"`
	}, 1)
	sub := startSubscription(context.Background(), inner, out, func(raw []byte) (*struct {
		Number string `json:"number"`
	}, error) {
		var value struct {
			Number string `json:"number"`
		}
		return &value, json.Unmarshal(raw, &value)
	})
	inner.events <- transport.Notification{Result: json.RawMessage(`{"number":"0x2"}`)}
	select {
	case value := <-out:
		if value.Number != "0x2" {
			t.Fatalf("number = %q", value.Number)
		}
	case <-time.After(time.Second):
		t.Fatal("event not forwarded")
	}
	inner.errs <- errors.New("boom")
	select {
	case err := <-sub.Err():
		if err == nil || err.Error() != "boom" {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("error not forwarded")
	}
	sub.Unsubscribe()
}

func TestAggregatedSubscriptionQuorumDeduplicatesSources(t *testing.T) {
	aStream := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
	bStream := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
	sources := []identifiedSubscriptionSource{
		&aggregateTestSource{id: "a", sub: aStream},
		&aggregateTestSource{id: "b", sub: bStream},
	}
	out := make(chan string, 2)
	sub, err := startAggregatedSubscription(context.Background(), sources, internalsubscription.KindNewHeads, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		var value struct {
			Number string `json:"number"`
			Hash   string `json:"hash"`
			Parent string `json:"parent"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return internalsubscription.Observation{}, "", err
		}
		contextKey, key, err := internalsubscription.CanonicalNewHeadKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, "", err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value.Number, nil
	})
	if err != nil {
		t.Fatalf("start aggregated: %v", err)
	}
	defer sub.Unsubscribe()
	payload := json.RawMessage(`{"number":"0x1","hash":"0xabc","parent":"0xdef"}`)
	aStream.events <- transport.Notification{Result: payload}
	aStream.events <- transport.Notification{Result: payload}
	bStream.events <- transport.Notification{Generation: 2, Result: payload}
	select {
	case got := <-out:
		t.Fatalf("stale generation delivered: %q", got)
	default:
	}
	bStream.events <- transport.Notification{Result: payload}
	select {
	case got := <-out:
		if got != "0x1" {
			t.Fatalf("output = %q, want 0x1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("quorum output not delivered")
	}
	select {
	case got := <-out:
		t.Fatalf("duplicate output = %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAggregatedSubscriptionReconnectsSourceAndKeepsSnapshot(t *testing.T) {
	firstA := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
	secondA := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
	b := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
	aSource := &reconnectTestSource{id: "a", subs: []transport.Subscription{firstA, secondA}}
	bSource := &reconnectTestSource{id: "b", subs: []transport.Subscription{b}}
	sources := []identifiedSubscriptionSource{aSource, bSource}
	out := make(chan string, 4)
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		var value struct{ Number, Hash, Parent string }
		if err := json.Unmarshal(raw, &value); err != nil {
			return internalsubscription.Observation{}, "", err
		}
		contextKey, key, err := internalsubscription.CanonicalNewHeadKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, "", err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value.Number, nil
	}
	sub, err := startAggregatedSubscription(context.Background(), sources, internalsubscription.KindNewHeads, nil, out, decode)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	first := json.RawMessage(`{"number":"0x1","hash":"0xabc","parent":"0xdef"}`)
	firstA.events <- transport.Notification{Result: first}
	b.events <- transport.Notification{Result: first}
	select {
	case got := <-out:
		if got != "0x1" {
			t.Fatalf("initial output = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("initial quorum output missing")
	}
	firstA.errs <- errors.New("socket lost")
	deadline := time.Now().Add(time.Second)
	for aSource.subscriptions() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if aSource.subscriptions() != 2 {
		t.Fatalf("reconnect attempts = %d, want 2", aSource.subscriptions())
	}
	second := json.RawMessage(`{"number":"0x2","hash":"0xabe","parent":"0xabc"}`)
	secondA.events <- transport.Notification{Result: second}
	b.events <- transport.Notification{Result: second}
	select {
	case got := <-out:
		if got != "0x2" {
			t.Fatalf("reconnected output = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnected quorum output missing")
	}
}
func TestAggregatedSubscriptionUnsubscribeWaitsForReconnectSetup(t *testing.T) {
	first := &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error, 1)}
	replacement := &trackedSubscription{
		events:       make(chan transport.Notification),
		errs:         make(chan error),
		unsubscribed: make(chan struct{}),
	}
	blocked := &blockingReconnectSource{
		id:            "a",
		first:         first,
		replacement:   replacement,
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	other := &aggregateTestSource{
		id:  "b",
		sub: &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)},
	}
	out := make(chan string)
	sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{blocked, other}, internalsubscription.KindNewHeads, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: "head:1", Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...)}, string(raw), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	first.errs <- errors.New("socket lost")
	select {
	case <-blocked.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("replacement subscription setup did not start")
	}

	unsubscribed := make(chan struct{})
	go func() {
		sub.Unsubscribe()
		close(unsubscribed)
	}()
	select {
	case <-unsubscribed:
		t.Fatal("Unsubscribe returned before the reconnect worker exited")
	case <-time.After(50 * time.Millisecond):
	}

	close(blocked.releaseSecond)
	select {
	case <-replacement.unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("replacement subscription was not cleaned up")
	}
	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("Unsubscribe did not wait for reconnect cleanup")
	}
}

func TestAggregatedSubscriptionCancellationUnblocksDelivery(t *testing.T) {
	a := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
	b := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
	out := make(chan string)
	sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{
		&aggregateTestSource{id: "a", sub: a},
		&aggregateTestSource{id: "b", sub: b},
	}, internalsubscription.KindNewHeads, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: "head:1", Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...)}, string(raw), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.events <- transport.Notification{Result: json.RawMessage(`"same"`)}
	b.events <- transport.Notification{Result: json.RawMessage(`"same"`)}

	done := make(chan struct{})
	go func() {
		sub.Unsubscribe()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Unsubscribe remained blocked by an unread delivery")
	}
}

func TestAggregatedSubscriptionPullProbeCompletesMissingSource(t *testing.T) {
	aStream := &aggregateTestSubscription{events: make(chan transport.Notification, 2), errs: make(chan error, 1)}
	bStream := &aggregateTestSubscription{events: make(chan transport.Notification, 2), errs: make(chan error, 1)}
	payload := json.RawMessage(`{"number":"0x1","hash":"0xabc","parent":"0xdef"}`)
	a := &pullTestSource{id: "a", stream: aStream, pullRaw: payload}
	b := &pullTestSource{id: "b", stream: bStream, pullRaw: payload}
	sources := []identifiedSubscriptionSource{a, b}
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		var value struct{ Number, Hash, Parent string }
		if err := json.Unmarshal(raw, &value); err != nil {
			return internalsubscription.Observation{}, "", err
		}
		contextKey, key, err := internalsubscription.CanonicalNewHeadKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, "", err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value.Number, nil
	}
	pullFactory := func(ctx context.Context, source identifiedSubscriptionSource, _ internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		unary := source.(subscriptionUnarySource)
		raw, err := unary.CallContext(ctx, "eth_getBlockByNumber", strings.TrimPrefix(contextKey, "head:"), false)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		obs, _, err := decode(raw, source.ID())
		obs.Generation = seed.Generation
		return obs, err
	}
	out := make(chan string, 1)
	sub, err := startAggregatedSubscriptionWithPull(context.Background(), sources, internalsubscription.KindNewHeads, nil, out, decode, pullFactory)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	// Model a reconnect of the missing source before its compensation Pull.
	// The Pull must use this physical generation rather than the normalized
	// logical generation carried by the pushed seed observation.
	if _, err := sub.(*aggregateEthSubscription[string]).sessions.Open("b"); err != nil {
		t.Fatal(err)
	}
	aStream.events <- transport.Notification{Result: payload}
	select {
	case got := <-out:
		if got != "0x1" {
			t.Fatalf("pull output = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("same-source pull did not complete quorum")
	}
	if b.pullCount() == 0 {
		t.Fatal("missing source was not pulled")
	}
}

func TestAggregatedNewHeadGapPullSuccessDoesNotTerminate(t *testing.T) {
	streamA := &aggregateTestSubscription{events: make(chan transport.Notification, 8), errs: make(chan error, 1)}
	streamB := &aggregateTestSubscription{events: make(chan transport.Notification, 8), errs: make(chan error, 1)}
	header := func(number string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"parentHash":"0x%s","sha3Uncles":"0x%s","stateRoot":"0x%s","transactionsRoot":"0x%s","receiptsRoot":"0x%s","logsBloom":"0x%s","difficulty":"0x1","number":"%s","gasLimit":"0x1","gasUsed":"0x0","timestamp":"0x1","extraData":"0x"}`,
			strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 256), number))
	}
	pull := func(method string, args ...any) (json.RawMessage, error) {
		if method != "eth_getBlockByNumber" || len(args) == 0 {
			return nil, fmt.Errorf("unexpected Pull %s", method)
		}
		return header(args[0].(string)), nil
	}
	a := &pullTestSource{id: "a", stream: streamA, pull: pull}
	b := &pullTestSource{id: "b", stream: streamB, pull: pull}
	out := make(chan *types.Header, 4)
	sub, err := startAggregatedSubscriptionWithPull(context.Background(), []identifiedSubscriptionSource{a, b}, internalsubscription.KindNewHeads, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, *types.Header, error) {
		h, err := typed.DecodeHeader(raw)
		if err != nil || h.Number == nil {
			return internalsubscription.Observation{}, nil, err
		}
		contextKey, key, err := internalsubscription.CanonicalNewHeadKey(h.Number.String(), h.Hash().Hex(), h.ParentHash.Hex())
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, h, nil
	}, func(ctx context.Context, source identifiedSubscriptionSource, _ internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		unary := source.(subscriptionUnarySource)
		raw, err := unary.CallContext(ctx, "eth_getBlockByNumber", strings.TrimPrefix(contextKey, "head:"), false)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		var value struct{ Number, Hash, Parent string }
		if err := json.Unmarshal(raw, &value); err != nil {
			return internalsubscription.Observation{}, err
		}
		keyContext, key, err := internalsubscription.CanonicalNewHeadKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: keyContext, Key: key, SourceID: source.ID(), Generation: seed.Generation, Payload: raw}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	for _, stream := range []*aggregateTestSubscription{streamA, streamB} {
		stream.events <- transport.Notification{Result: header("0x1")}
	}
	for _, stream := range []*aggregateTestSubscription{streamA, streamB} {
		stream.events <- transport.Notification{Result: header("0x3")}
	}
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("head 3 was not delivered")
	}
	time.Sleep(250 * time.Millisecond)
	select {
	case err := <-sub.Err():
		t.Fatalf("successful gap Pull terminated subscription: %v", err)
	default:
	}
}

func TestAggregatedNewHeadGapPullFailureReturnsStructuredError(t *testing.T) {
	streamA := &aggregateTestSubscription{events: make(chan transport.Notification, 8), errs: make(chan error, 1)}
	streamB := &aggregateTestSubscription{events: make(chan transport.Notification, 8), errs: make(chan error, 1)}
	header := func(number string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"parentHash":"0x%s","sha3Uncles":"0x%s","stateRoot":"0x%s","transactionsRoot":"0x%s","receiptsRoot":"0x%s","logsBloom":"0x%s","difficulty":"0x1","number":"%s","gasLimit":"0x1","gasUsed":"0x0","timestamp":"0x1","extraData":"0x"}`,
			strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 32), strings.Repeat("00", 256), number))
	}
	gapCause := errors.New("gap Pull failed")
	failedPull := func(string, ...any) (json.RawMessage, error) { return nil, gapCause }
	a := &pullTestSource{id: "a", stream: streamA, pull: failedPull}
	b := &pullTestSource{id: "b", stream: streamB, pull: failedPull}
	out := make(chan *types.Header, 2)
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, *types.Header, error) {
		h, err := typed.DecodeHeader(raw)
		if err != nil || h.Number == nil {
			return internalsubscription.Observation{}, nil, err
		}
		contextKey, key, err := internalsubscription.CanonicalNewHeadKey(h.Number.String(), h.Hash().Hex(), h.ParentHash.Hex())
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: raw}, h, nil
	}
	sub, err := startAggregatedSubscriptionWithPull(context.Background(), []identifiedSubscriptionSource{a, b}, internalsubscription.KindNewHeads, nil, out, decode, func(context.Context, identifiedSubscriptionSource, internalsubscription.Kind, string, internalsubscription.Observation) (internalsubscription.Observation, error) {
		return internalsubscription.Observation{}, errors.New("event Pull failed")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	for _, stream := range []*aggregateTestSubscription{streamA, streamB} {
		stream.events <- transport.Notification{Result: header("0x1")}
	}
	for _, stream := range []*aggregateTestSubscription{streamA, streamB} {
		stream.events <- transport.Notification{Result: header("0x3")}
	}
	var gapErr *SubscriptionGapError
	select {
	case err := <-sub.Err():
		if !errors.As(err, &gapErr) {
			t.Fatalf("err = %v, want SubscriptionGapError", err)
		}
		if gapErr.SourceID == "" || gapErr.From != 2 || gapErr.To != 2 || gapErr.ContextKey != "head:3" || gapErr.Generation == 0 {
			t.Fatalf("gap error = %+v", gapErr)
		}
		if !errors.Is(err, ErrSubscriptionGap) {
			t.Fatalf("err = %v, want ErrSubscriptionGap", err)
		}
		if !errors.Is(err, gapCause) {
			t.Fatalf("err = %v, want underlying gap cause", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gap error was not reported")
	}
}

func TestAggregatedTransactionReceiptsQuorumAndConflict(t *testing.T) {
	newSources := func() ([]identifiedSubscriptionSource, *aggregateTestSubscription, *aggregateTestSubscription) {
		aStream := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
		bStream := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
		return []identifiedSubscriptionSource{
			&aggregateTestSource{id: "a", sub: aStream},
			&aggregateTestSource{id: "b", sub: bStream},
		}, aStream, bStream
	}
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, []*types.Receipt, error) {
		receipts, err := typed.DecodeReceipts(raw)
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		ids := make([]internalsubscription.ReceiptIdentity, len(receipts))
		for i, receipt := range receipts {
			canonical, marshalErr := json.Marshal(receipt)
			if marshalErr != nil {
				return internalsubscription.Observation{}, nil, marshalErr
			}
			ids[i] = internalsubscription.ReceiptIdentity{TransactionHash: receipt.TxHash.Hex(), BlockHash: receipt.BlockHash.Hex(), BlockNumber: receipt.BlockNumber.String(), TransactionIndex: uint64(receipt.TransactionIndex), Canonical: canonical}
		}
		contextKey, key, err := internalsubscription.CanonicalTransactionReceiptsKey(ids)
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindTransactionReceipts, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, receipts, nil
	}
	receiptPayload := func(status uint64) json.RawMessage {
		txHash := common.HexToHash("0x" + "11" + strings.Repeat("00", 31))
		blockHash := common.HexToHash("0x" + "22" + strings.Repeat("00", 31))
		receipt := &types.Receipt{Status: status, CumulativeGasUsed: 21_000, GasUsed: 21_000, TxHash: txHash, BlockHash: blockHash, BlockNumber: big.NewInt(7), TransactionIndex: 0, Logs: []*types.Log{}}
		receipt.Bloom = types.CreateBloom(receipt)
		raw, err := json.Marshal([]*types.Receipt{receipt})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	t.Run("same batch reaches quorum once", func(t *testing.T) {
		sources, aStream, bStream := newSources()
		out := make(chan []*types.Receipt, 2)
		sub, err := startAggregatedSubscription(context.Background(), sources, internalsubscription.KindTransactionReceipts, nil, out, decode)
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Unsubscribe()
		payload := receiptPayload(types.ReceiptStatusSuccessful)
		aStream.events <- transport.Notification{Result: payload}
		aStream.events <- transport.Notification{Result: payload}
		bStream.events <- transport.Notification{Result: payload}
		select {
		case got := <-out:
			if len(got) != 1 || got[0].Status != types.ReceiptStatusSuccessful {
				t.Fatalf("receipts = %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("receipt quorum output missing")
		}
		select {
		case <-out:
			t.Fatal("duplicate receipt batch delivered")
		case <-time.After(50 * time.Millisecond):
		}
	})

	t.Run("conflicting batch is quarantined", func(t *testing.T) {
		sources, aStream, bStream := newSources()
		out := make(chan []*types.Receipt, 2)
		sub, err := startAggregatedSubscription(context.Background(), sources, internalsubscription.KindTransactionReceipts, nil, out, decode)
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Unsubscribe()
		aStream.events <- transport.Notification{Result: receiptPayload(types.ReceiptStatusSuccessful)}
		bStream.events <- transport.Notification{Result: receiptPayload(types.ReceiptStatusFailed)}
		select {
		case got := <-out:
			t.Fatalf("conflicting receipts delivered: %+v", got)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func ptrHash(hash common.Hash) *common.Hash { return &hash }

// blockingEstablishSource holds the establish loop inside Subscribe so a
// test can interleave an owner close between the loop's stop check and its
// s.inner publication.
type blockingEstablishSource struct {
	id      string
	sub     *trackedSubscription
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingEstablishSource) ID() string { return s.id }

func (s *blockingEstablishSource) Subscribe(context.Context, string, ...any) (transport.Subscription, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return s.sub, nil
}

// TestAggregatedSubscriptionEstablishRacesOwnerCloseWithoutLeak pins the
// initial-establish half of the inner-publication invariant: an inner
// subscription that lands after finish snapshotted s.inner must unsubscribe
// itself, exactly like the reconnect path's stop recheck. The reconnect
// half is covered by TestAggregatedSubscriptionUnsubscribeWaitsForReconnectSetup.
func TestAggregatedSubscriptionEstablishRacesOwnerCloseWithoutLeak(t *testing.T) {
	blocked := &blockingEstablishSource{
		id: "a",
		sub: &trackedSubscription{
			events:       make(chan transport.Notification),
			errs:         make(chan error),
			unsubscribed: make(chan struct{}),
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	other := &aggregateTestSource{
		id:  "b",
		sub: &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)},
	}
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	defer ownerCancel()
	out := make(chan string)
	type outcome struct {
		sub ethereum.Subscription
		err error
	}
	results := make(chan outcome, 1)
	go func() {
		sub, err := startAggregatedSubscriptionWithPullOwner(ownerCtx, context.Background(), []identifiedSubscriptionSource{blocked, other}, internalsubscription.KindNewHeads, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
			return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: "head:1", Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...)}, string(raw), nil
		}, nil)
		results <- outcome{sub: sub, err: err}
	}()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("establish loop never reached the first source")
	}
	// The owner closes while source "a"'s Subscribe is still in flight.
	// Yield long enough for the watcher's finish to land, so the loop's
	// write below happens after the snapshot — the interleaving the stop
	// recheck exists for. (Same timing-assumption class as the 50ms
	// negative assertion in TestAggregatedSubscriptionUnsubscribeWaitsForReconnectSetup.)
	ownerCancel()
	time.Sleep(50 * time.Millisecond)
	close(blocked.release)
	// Invariant: the inner subscription is unsubscribed no matter which
	// side of the snapshot the publication landed on.
	select {
	case <-blocked.sub.unsubscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("inner subscription leaked: nobody unsubscribed after owner close during establish")
	}
	var res outcome
	select {
	case res = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("establish loop did not return after owner close")
	}
	if res.err == nil && res.sub != nil {
		// A handle that escaped the race must still terminate promptly.
		done := make(chan struct{})
		go func() {
			res.sub.Unsubscribe()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Unsubscribe blocked on a handle returned after owner close")
		}
	}
}
