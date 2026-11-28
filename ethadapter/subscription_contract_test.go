package ethadapter_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethadapter"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	bind "github.com/ethereum/go-ethereum/accounts/abi/bind/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	_ ethereum.ChainReader       = (*ethadapter.Client)(nil)
	_ ethereum.TransactionReader = (*ethadapter.Client)(nil)
	_ ethereum.LogFilterer       = (*ethadapter.Client)(nil)
	_ bind.ContractFilterer      = (*ethadapter.Client)(nil)
	_ bind.ContractBackend       = (*ethadapter.Client)(nil)
)

func TestIndependentLockedGethFullBusinessMethodSurface(t *testing.T) {
	geth, adapter := reflect.TypeFor[*ethclient.Client](), reflect.TypeFor[*ethadapter.Client]()
	count := 0
	for i := range geth.NumMethod() {
		want := geth.Method(i)
		if want.Name == "Client" {
			continue
		}
		count++
		got, ok := adapter.MethodByName(want.Name)
		if !ok {
			t.Errorf("missing %s", want.Name)
			continue
		}
		if got.Type.NumIn() != want.Type.NumIn() || got.Type.NumOut() != want.Type.NumOut() {
			t.Errorf("signature differs for %s", want.Name)
			continue
		}
		for j := 1; j < want.Type.NumIn(); j++ {
			if got.Type.In(j) != want.Type.In(j) {
				t.Errorf("%s input %d differs", want.Name, j)
			}
		}
		for j := range want.Type.NumOut() {
			if got.Type.Out(j) != want.Type.Out(j) {
				t.Errorf("%s output %d differs", want.Name, j)
			}
		}
	}
	if count != 47 {
		t.Fatalf("locked full business inventory=%d want47", count)
	}
}

type bridgeSink struct {
	kind     string
	heads    chan *types.Header
	logs     chan types.Log
	receipts chan []*types.Receipt
	query    ethereum.FilterQuery
}

func newBridgeSink(kind string, capacity int) *bridgeSink {
	return &bridgeSink{kind: kind, heads: make(chan *types.Header, capacity), logs: make(chan types.Log, capacity), receipts: make(chan []*types.Receipt, capacity)}
}
func (s *bridgeSink) subscribe(ctx context.Context, c *ethadapter.Client) (ethereum.Subscription, error) {
	switch s.kind {
	case "heads":
		return c.SubscribeNewHead(ctx, s.heads)
	case "logs":
		return c.SubscribeFilterLogs(ctx, s.query, s.logs)
	case "receipts":
		return c.SubscribeTransactionReceipts(ctx, nil, s.receipts)
	default:
		panic("invalid fixture stream")
	}
}
func (s *bridgeSink) next(t *testing.T) common.Hash {
	t.Helper()
	switch s.kind {
	case "heads":
		return bridgeAwait(t, s.heads).Hash()
	case "logs":
		return bridgeAwait(t, s.logs).BlockHash
	case "receipts":
		rows := bridgeAwait(t, s.receipts)
		if len(rows) == 0 {
			t.Fatal("expected fixture receipt")
		}
		return rows[0].BlockHash
	default:
		panic("invalid fixture stream")
	}
}
func (s *bridgeSink) assertOpenAndEmpty(t *testing.T) {
	t.Helper()
	switch s.kind {
	case "heads":
		select {
		case _, ok := <-s.heads:
			if !ok {
				t.Fatal("adapter closed caller heads channel")
			}
			t.Fatal("unexpected post-terminal head")
		default:
		}
	case "logs":
		select {
		case _, ok := <-s.logs:
			if !ok {
				t.Fatal("adapter closed caller logs channel")
			}
			t.Fatal("unexpected post-terminal log")
		default:
		}
	case "receipts":
		select {
		case _, ok := <-s.receipts:
			if !ok {
				t.Fatal("adapter closed caller receipts channel")
			}
			t.Fatal("unexpected post-terminal receipt")
		default:
		}
	}
}

func bridgeClient(t *testing.T, peers ...*bridgePeer) *ethadapter.Client {
	t.Helper()
	endpoints := make([]string, len(peers))
	for i, p := range peers {
		endpoints[i] = p.url
	}
	client, err := ethadapter.Dial(endpoints...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}
func bridgeTerminal(t *testing.T, sub ethereum.Subscription, want error) {
	t.Helper()
	select {
	case err, ok := <-sub.Err():
		if want != nil {
			if !ok || !errors.Is(err, want) {
				t.Fatalf("terminal=%v ok=%v want %v", err, ok, want)
			}
		} else if ok && err != nil {
			t.Fatalf("active stop acquired failure: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("terminal channel blocked behind unread delivery")
	}
	select {
	case _, ok := <-sub.Err():
		if ok {
			t.Fatal("more than one terminal error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal channel was not closed")
	}
}
func bridgeWaitClose(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("close barrier did not finish")
	}
}

func TestIndependentBridgeNormalProgressAndSetupContextLifetime(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		for _, sources := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/sources_%d", kind, sources), func(t *testing.T) {
				first := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
				second := bridgeFixture(t, first.header.Hash(), 18, 2, 1, 1)
				peers := make([]*bridgePeer, sources)
				for i := range peers {
					peers[i] = newBridgePeer(t, first, second)
				}
				client := bridgeClient(t, peers...)
				sink := newBridgeSink(kind, 0)
				setup, cancel := context.WithCancel(t.Context())
				defer cancel()
				sub, err := sink.subscribe(setup, client)
				if err != nil {
					t.Fatal(err)
				}
				defer sub.Unsubscribe()
				for _, peer := range peers {
					peer.push(t, first)
				}
				if got := sink.next(t); got != first.header.Hash() {
					t.Fatalf("first block=%s", got)
				}
				cancel()
				for _, peer := range peers {
					peer.push(t, second)
				}
				if got := sink.next(t); got != second.header.Hash() {
					t.Fatalf("construction cancellation killed stream or reordered block: %s", got)
				}
				bridgeWaitClose(t, sub.Unsubscribe)
				bridgeTerminal(t, sub, nil)
				sink.assertOpenAndEmpty(t)
			})
		}
	}
}

func TestIndependentBridgeNativeRetractionTerminatesEveryKind(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			base := bridgeFixture(t, common.Hash{}, 16, 1, 1, 1)
			first := bridgeFixture(t, base.header.Hash(), 17, 2, 1, 1)
			fork := bridgeFixture(t, base.header.Hash(), 17, 3, 1, 1)
			peer := newBridgePeer(t, base, first, fork)
			client := bridgeClient(t, peer)
			sink := newBridgeSink(kind, 0)
			sub, err := sink.subscribe(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Unsubscribe()
			peer.push(t, base)
			if sink.next(t) != base.header.Hash() {
				t.Fatal("wrong base")
			}
			peer.push(t, first)
			if sink.next(t) != first.header.Hash() {
				t.Fatal("wrong forward block")
			}
			peer.push(t, fork)
			bridgeTerminal(t, sub, ethguard.ErrReorgDetected)
			sink.assertOpenAndEmpty(t)
		})
	}
}

func TestIndependentBridgeZeroMatchLogsStillProtectPublishedPath(t *testing.T) {
	base := bridgeFixture(t, common.Hash{}, 16, 0xf0, 2, 2)
	first := bridgeFixture(t, base.header.Hash(), 17, 0xf0, 2, 2)
	// Use a distinct valid alternate header while retaining a false-positive
	// bloom: address and topic each occur, but never in the same log.
	fork := bridgeFixture(t, base.header.Hash(), 17, 0xf0, 3, 2)
	peer := newBridgePeer(t, base, first, fork)
	audits := make(chan bridgeAuditRecord, 8)
	client := bridgeObservedClient(t, peer, func(_ context.Context, ev ethadapter.AuditEvent) { audits <- bridgeAuditSnapshot(ev) })
	sink := newBridgeSink("logs", 0)
	sink.query = ethereum.FilterQuery{Addresses: []common.Address{common.HexToAddress("0x1234")}, Topics: [][]common.Hash{{crypto.Keccak256Hash([]byte("Other(uint256)"))}}}
	sub, err := sink.subscribe(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if bridgeAwait(t, audits).phase != "setup" {
		t.Fatal("missing zero-match setup")
	}
	for _, block := range []*bridgeBlock{base, first} {
		peer.push(t, block)
		if event := bridgeAwait(t, audits); event.phase != "event" || event.appended != 1 || event.logCount != 0 {
			t.Fatalf("zero-match block did not commit path: %+v", event)
		}
	}
	sink.assertOpenAndEmpty(t)
	peer.push(t, fork)
	bridgeTerminal(t, sub, ethguard.ErrReorgDetected)
	sink.assertOpenAndEmpty(t)
}

func TestIndependentBridgeUnreadDeliveryStopsAtEveryPublicBarrier(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		for _, stop := range []string{"unsubscribe", "adapter_close", "core_close"} {
			t.Run(kind+"/"+stop, func(t *testing.T) {
				first := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
				second := bridgeFixture(t, first.header.Hash(), 18, 2, 1, 1)
				peer := newBridgePeer(t, first, second)
				audits := make(chan bridgeAuditRecord, 8)
				client := bridgeObservedClient(t, peer, func(_ context.Context, ev ethadapter.AuditEvent) { audits <- bridgeAuditSnapshot(ev) })
				sink := newBridgeSink(kind, 0)
				sub, err := sink.subscribe(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
				defer sub.Unsubscribe()
				if bridgeAwait(t, audits).phase != "setup" {
					t.Fatal("missing setup audit")
				}
				peer.push(t, first)
				if sink.next(t) != first.header.Hash() {
					t.Fatal("wrong birth block")
				}
				if bridgeAwait(t, audits).phase != "event" {
					t.Fatal("missing birth event audit")
				}
				peer.push(t, second)
				if event := bridgeAwait(t, audits); event.phase != "event" || event.appended != 1 {
					t.Fatalf("unread delivery was never produced: %+v", event)
				}
				switch stop {
				case "unsubscribe":
					bridgeWaitClose(t, sub.Unsubscribe)
				case "adapter_close":
					bridgeWaitClose(t, client.Close)
				case "core_close":
					bridgeWaitClose(t, client.Core().Close)
				}
				bridgeTerminal(t, sub, nil)
				sink.assertOpenAndEmpty(t)
			})
		}
	}
}

func TestIndependentBridgeCloseRacesRegistrationAndSourceSetup(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			peer := newBridgePeer(t)
			gate := make(chan struct{})
			peer.mu.Lock()
			peer.gate = gate
			peer.mu.Unlock()
			defer close(gate)
			client := bridgeClient(t, peer)
			sink := newBridgeSink(kind, 0)
			sub, err := sink.subscribe(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			peer.wait(t, func(requests []bridgeRequest, _ int) bool {
				for _, r := range requests {
					if r.Method == "eth_subscribe" {
						return true
					}
				}
				return false
			})
			bridgeWaitClose(t, client.Core().Close)
			bridgeTerminal(t, sub, nil)
		})
	}
	for range 20 {
		peer := newBridgePeer(t)
		client := bridgeClient(t, peer)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, kind := range []string{"heads", "logs", "receipts"} {
			wg.Go(func() {
				<-start
				sink := newBridgeSink(kind, 0)
				sub, err := sink.subscribe(t.Context(), client)
				if err != nil {
					if !errors.Is(err, ethguard.ErrPoolClosed) {
						t.Errorf("close/setup race: %v", err)
					}
					return
				}
				sub.Unsubscribe()
			})
		}
		wg.Go(func() { <-start; client.Close() })
		close(start)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("registration race leaked work")
		}
	}
}

func TestIndependentBridgeCanceledSetupRejectsBeforeNetwork(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			peer := newBridgePeer(t)
			client := bridgeClient(t, peer)
			sink := newBridgeSink(kind, 0)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			sub, err := sink.subscribe(ctx, client)
			if !errors.Is(err, context.Canceled) || sub != nil {
				if sub != nil {
					sub.Unsubscribe()
				}
				t.Fatalf("canceled setup=%v %v", sub, err)
			}
			peer.mu.Lock()
			count := len(peer.requests)
			peer.mu.Unlock()
			if count != 0 {
				t.Fatal("canceled setup contacted upstream")
			}
			bridgeWaitClose(t, client.Close)
		})
	}
}

func TestIndependentBindContractWatchConsumesAdapterLogs(t *testing.T) {
	block := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
	peer := newBridgePeer(t, block)
	client := bridgeClient(t, peer)
	contractABI, err := abi.JSON(strings.NewReader(`[{"anonymous":false,"inputs":[{"indexed":false,"name":"value","type":"uint256"}],"name":"Changed","type":"event"}]`))
	if err != nil {
		t.Fatal(err)
	}
	contract := bind.NewBoundContract(common.HexToAddress("0x1234"), contractABI, client, client, client)
	logs, sub, err := contract.WatchLogs(&bind.WatchOpts{Context: t.Context()}, "Changed")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	peer.push(t, block)
	log := bridgeAwait(t, logs)
	var decoded struct{ Value *big.Int }
	if err := contract.UnpackLog(&decoded, "Changed", log); err != nil {
		t.Fatal(err)
	}
	if decoded.Value.Cmp(big.NewInt(1)) != 0 || log.BlockHash != block.header.Hash() {
		t.Fatalf("bound event lost value or inclusion: %+v %+v", decoded, log)
	}
	bridgeWaitClose(t, sub.Unsubscribe)
	bridgeTerminal(t, sub, nil)
}

func TestIndependentBridgeReceiptQueryIsFrozenBeforeCallerMutation(t *testing.T) {
	block := bridgeFixture(t, common.Hash{}, 17, 1, 2, 2)
	peer := newBridgePeer(t, block)
	client := bridgeClient(t, peer)
	out := make(chan []*types.Receipt)
	query := &ethereum.TransactionReceiptsQuery{TransactionHashes: []common.Hash{block.receipts[1].TxHash}}
	sub, err := client.SubscribeTransactionReceipts(t.Context(), query, out)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	query.TransactionHashes[0] = block.receipts[0].TxHash
	query.TransactionHashes = append(query.TransactionHashes, block.receipts[0].TxHash)
	peer.push(t, block)
	got := bridgeAwait(t, out)
	if len(got) != 1 || !reflect.DeepEqual(got[0], block.receipts[1]) {
		t.Fatalf("caller mutation changed frozen receipt selection: %+v", got)
	}
	bridgeWaitClose(t, sub.Unsubscribe)
	bridgeTerminal(t, sub, nil)
}

func TestIndependentDefaultBridgeHandlesRepresentativeLargeBlock(t *testing.T) {
	block := bridgeFixture(t, common.Hash{}, 17, 1, 240, 720)
	if len(block.receipts) != 240 || len(block.logs) != 720 || len(block.logs[0].Data) != 128 || len(block.logs[0].Topics) != 3 {
		t.Fatal("representative fixture lost required load")
	}
	t.Logf("independent large block: transactions=240 logs=720 block_bytes=%d receipt_bytes=%d", len(block.blockRaw), len(block.receiptsRaw))
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			peer := newBridgePeer(t, block)
			client := bridgeClient(t, peer)
			sink := newBridgeSink(kind, 0)
			sub, err := sink.subscribe(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Unsubscribe()
			peer.push(t, block)
			switch kind {
			case "heads":
				if sink.next(t) != block.header.Hash() {
					t.Fatal("wrong large header")
				}
			case "logs":
				for i := range block.logs {
					got := bridgeAwait(t, sink.logs)
					if !reflect.DeepEqual(got, block.logs[i]) {
						t.Fatalf("large log %d changed", i)
					}
				}
			case "receipts":
				rows := bridgeAwait(t, sink.receipts)
				if !reflect.DeepEqual(rows, block.receipts) {
					t.Fatal("large receipt set changed")
				}
			}
			bridgeWaitClose(t, sub.Unsubscribe)
			bridgeTerminal(t, sub, nil)
		})
	}
}
