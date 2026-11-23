package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
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

func TestSubscriptionFilterArgMatchesOfficialShape(t *testing.T) {
	rangeQuery, err := ethapi.NewFilterQuery(json.RawMessage(`{"address":["0x0100000000000000000000000000000000000000"],"topics":[["0x0200000000000000000000000000000000000000000000000000000000000000"]]}`))
	if err != nil {
		t.Fatalf("filter query: %v", err)
	}
	arg, err := toFilterArg(rangeQuery)
	if err != nil {
		t.Fatalf("filter arg: %v", err)
	}
	if _, ok := arg["fromBlock"]; !ok {
		t.Fatal("missing default fromBlock")
	}
	if _, ok := arg["toBlock"]; !ok {
		t.Fatal("missing default toBlock")
	}
	// The hash branch is an open object, so fromBlock parses there; the
	// method's own mutual exclusion must reject it before any contact.
	hashQuery, err := ethapi.NewFilterQuery(json.RawMessage(`{"blockHash":"0x0100000000000000000000000000000000000000000000000000000000000000","fromBlock":"0x1"}`))
	if err != nil {
		t.Fatalf("hash query: %v", err)
	}
	if _, err := toFilterArg(hashQuery); err == nil {
		t.Fatal("accepted block hash with range")
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
	sub, err := startAggregatedSubscription(context.Background(), sources, internalsubscription.KindLogs, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		var value struct {
			Number string `json:"number"`
			Hash   string `json:"hash"`
			Parent string `json:"parent"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return internalsubscription.Observation{}, "", err
		}
		contextKey, key, err := canonicalTestLogKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, "", err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value.Number, nil
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
		contextKey, key, err := canonicalTestLogKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, "", err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value.Number, nil
	}
	sub, err := startAggregatedSubscription(context.Background(), sources, internalsubscription.KindLogs, nil, out, decode)
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
	sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{blocked, other}, internalsubscription.KindLogs, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: "log:1", Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...)}, string(raw), nil
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
	}, internalsubscription.KindLogs, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
		return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: "log:1", Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...)}, string(raw), nil
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
		contextKey, key, err := canonicalTestLogKey(value.Number, value.Hash, value.Parent)
		if err != nil {
			return internalsubscription.Observation{}, "", err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value.Number, nil
	}
	pullFactory := func(ctx context.Context, source identifiedSubscriptionSource, _ internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		unary := source.(subscriptionUnarySource)
		raw, err := unary.CallContext(ctx, "eth_getBlockByNumber", strings.TrimPrefix(contextKey, "log:"), false)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		obs, _, err := decode(raw, source.ID())
		obs.Generation = seed.Generation
		return obs, err
	}
	out := make(chan string, 1)
	sub, err := startAggregatedSubscriptionWithPull(context.Background(), sources, internalsubscription.KindLogs, nil, out, decode, pullFactory)
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

func TestAggregatedTransactionReceiptsQuorumAndConflict(t *testing.T) {
	newSources := func() ([]identifiedSubscriptionSource, *aggregateTestSubscription, *aggregateTestSubscription) {
		aStream := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
		bStream := &aggregateTestSubscription{events: make(chan transport.Notification, 4), errs: make(chan error, 1)}
		return []identifiedSubscriptionSource{
			&aggregateTestSource{id: "a", sub: aStream},
			&aggregateTestSource{id: "b", sub: bStream},
		}, aStream, bStream
	}
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, []ethapi.Receipt, error) {
		values, canonical, err := typed.DecodeReceiptBatch(raw)
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(canonical, &rows); err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		ids := make([]internalsubscription.ReceiptIdentity, len(values))
		for i := range values {
			height, heightErr := typed.QuantityHeight(values[i].BlockNumber)
			if heightErr != nil {
				return internalsubscription.Observation{}, nil, heightErr
			}
			index, indexErr := typed.QuantityHeight(values[i].TransactionIndex)
			if indexErr != nil {
				return internalsubscription.Observation{}, nil, indexErr
			}
			ids[i] = internalsubscription.ReceiptIdentity{TransactionHash: string(values[i].TransactionHash), BlockHash: string(values[i].BlockHash), BlockNumber: strconv.FormatUint(height, 10), TransactionIndex: index, Canonical: rows[i]}
		}
		contextKey, key, err := internalsubscription.CanonicalTransactionReceiptsKey(ids)
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindTransactionReceipts, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, values, nil
	}
	receiptPayload := func(status uint64) json.RawMessage {
		txHash := common.HexToHash("0x" + "11" + strings.Repeat("00", 31))
		blockHash := common.HexToHash("0x" + "22" + strings.Repeat("00", 31))
		receipt := &types.Receipt{Status: status, CumulativeGasUsed: 21_000, GasUsed: 21_000, EffectiveGasPrice: big.NewInt(1), TxHash: txHash, BlockHash: blockHash, BlockNumber: big.NewInt(7), TransactionIndex: 0, Logs: []*types.Log{}}
		receipt.Bloom = types.CreateBloom(receipt)
		raw, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		object["from"] = json.RawMessage(`"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
		delete(object, "root")
		raw, err = json.Marshal([]map[string]json.RawMessage{object})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	t.Run("same batch reaches quorum once", func(t *testing.T) {
		sources, aStream, bStream := newSources()
		out := make(chan []ethapi.Receipt, 2)
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
			if len(got) != 1 || got[0].Status == nil || *got[0].Status != "0x1" {
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
		out := make(chan []ethapi.Receipt, 2)
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
		sub, err := startAggregatedSubscriptionWithPullOwner(ownerCtx, context.Background(), []identifiedSubscriptionSource{blocked, other}, internalsubscription.KindLogs, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
			return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: "log:1", Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...)}, string(raw), nil
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

type subscriptionRetryNetworkError struct {
	timeout   bool
	temporary bool
}

func (subscriptionRetryNetworkError) Error() string     { return "network failure" }
func (e subscriptionRetryNetworkError) Timeout() bool   { return e.timeout }
func (e subscriptionRetryNetworkError) Temporary() bool { return e.temporary }

func TestRetryableSubscriptionPullNetworkErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "wrapped timeout", err: fmt.Errorf("pull: %w", subscriptionRetryNetworkError{timeout: true}), want: true},
		{name: "wrapped temporary", err: fmt.Errorf("pull: %w", subscriptionRetryNetworkError{temporary: true}), want: true},
		{name: "permanent network error", err: subscriptionRetryNetworkError{}, want: false},
		{name: "owner canceled", err: context.Canceled, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableSubscriptionPullError(tc.err); got != tc.want {
				t.Fatalf("retryableSubscriptionPullError(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

type blockingCleanupSubscription struct {
	aggregateTestSubscription
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (s *blockingCleanupSubscription) Unsubscribe() {
	s.once.Do(func() {
		close(s.started)
		<-s.release
		close(s.finished)
	})
}

func decodeTestEvent(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
	return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: "log:" + string(raw), Key: string(raw), SourceID: sourceID, Payload: append([]byte(nil), raw...), ObservedAt: time.Now()}, string(raw), nil
}

func TestAggregatedSubscriptionUnsubscribeWaitsForInnerCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blocked := &blockingCleanupSubscription{
			aggregateTestSubscription: aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)},
			started:                   make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
		}
		release := sync.OnceFunc(func() { close(blocked.release) })
		other := &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)}
		sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{
			&aggregateTestSource{id: "a", sub: blocked}, &aggregateTestSource{id: "b", sub: other},
		}, internalsubscription.KindLogs, nil, make(chan string), decodeTestEvent)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { release(); sub.Unsubscribe() }()
		stopped := make(chan struct{})
		go func() { sub.Unsubscribe(); close(stopped) }()
		synctest.Wait()
		select {
		case <-blocked.started:
		default:
			t.Fatal("inner cleanup did not start")
		}
		select {
		case <-stopped:
			t.Fatal("Unsubscribe returned before inner cleanup finished")
		default:
		}
		release()
		synctest.Wait()
		select {
		case <-stopped:
		default:
			t.Fatal("Unsubscribe did not join completed inner cleanup")
		}
		select {
		case <-blocked.finished:
		default:
			t.Fatal("inner cleanup was not completed")
		}
	})
}

func TestAggregatedSubscriptionUnsubscribeWaitsForPullExit(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("delivered=%v", delivered), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
				b := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
				started, canceled, releasePull, exited := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
				release := sync.OnceFunc(func() { close(releasePull) })
				out := make(chan string, 1)
				sub, err := startAggregatedSubscriptionWithPull(context.Background(), []identifiedSubscriptionSource{
					&aggregateTestSource{id: "a", sub: a}, &aggregateTestSource{id: "b", sub: b},
				}, internalsubscription.KindLogs, nil, out, decodeTestEvent, func(ctx context.Context, source identifiedSubscriptionSource, _ internalsubscription.Kind, _ string, _ internalsubscription.Observation) (internalsubscription.Observation, error) {
					if source.ID() != "b" {
						return internalsubscription.Observation{}, fmt.Errorf("unexpected Pull source %s", source.ID())
					}
					close(started)
					<-ctx.Done()
					close(canceled)
					<-releasePull
					close(exited)
					return internalsubscription.Observation{}, ctx.Err()
				})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { release(); sub.Unsubscribe() }()
				a.events <- transport.Notification{Result: json.RawMessage(`1`)}
				synctest.Wait()
				time.Sleep(200 * time.Millisecond)
				synctest.Wait()
				select {
				case <-started:
				default:
					t.Fatal("ordinary Pull did not start")
				}
				if delivered {
					b.events <- transport.Notification{Result: json.RawMessage(`1`)}
					synctest.Wait()
					select {
					case <-out:
					default:
						t.Fatal("push quorum did not complete while Pull was in flight")
					}
				}
				stopped := make(chan struct{})
				go func() { sub.Unsubscribe(); close(stopped) }()
				synctest.Wait()
				select {
				case <-canceled:
				default:
					t.Fatal("Pull was not canceled")
				}
				select {
				case <-stopped:
					t.Fatal("Unsubscribe returned before ordinary Pull exited")
				default:
				}
				release()
				synctest.Wait()
				select {
				case <-stopped:
				default:
					t.Fatal("Unsubscribe did not join Pull exit")
				}
				select {
				case <-exited:
				default:
					t.Fatal("Pull cleanup did not finish")
				}
			})
		})
	}
}

func canonicalTestLogKey(number, hash, transaction string) (string, string, error) {
	return internalsubscription.CanonicalLogKey(hash, number, transaction, 0, "0xaddr", "0x", nil, false)
}
