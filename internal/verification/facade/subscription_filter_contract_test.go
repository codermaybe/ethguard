package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum"
)

// receiptFilterFixture builds a receipt-subscription query from caller-side
// hash spellings.
func receiptFilterFixture(t *testing.T, hashes ...string) *ethapi.TransactionReceiptsQuery {
	t.Helper()
	list := make([]ethapi.Hash, 0, len(hashes))
	for _, hash := range hashes {
		list = append(list, ethapi.Hash(hash))
	}
	return &ethapi.TransactionReceiptsQuery{TransactionHashes: &list}
}

// TestSubscribeTransactionReceiptsRejectsInvalidFilterBeforeEstablishing pins
// S1: an invalid hash inside a non-empty filter is an input error. Neither
// surface establishes anything — no registration request may reach any peer,
// and the error must wrap ErrInvalidRequest. The legal empty filter stays the
// official empty object on the wire.
func TestSubscribeTransactionReceiptsRejectsInvalidFilterBeforeEstablishing(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(fmt.Sprintf("view=%t", view), func(t *testing.T) {
			a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			client := subscriptionClient(t, a, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			invalid := receiptFilterFixture(t, "0x"+strings.Repeat("42", 32), "0x1")
			out := make(chan []ethapi.Receipt, 1)
			var err error
			if view {
				_, err = client.EthClient().SubscribeTransactionReceipts(ctx, invalid, out)
			} else {
				_, err = client.SubscribeTransactionReceipts(ctx, invalid, out)
			}
			if !errors.Is(err, ethguard.ErrInvalidRequest) {
				t.Fatalf("invalid filter error = %v, want ErrInvalidRequest", err)
			}
			for _, peer := range []*subscriptionPeer{a, b} {
				for _, req := range peer.requestedMethods() {
					if req == "eth_subscribe" {
						t.Fatalf("invalid filter still registered a subscription: %v", peer.requestedMethods())
					}
				}
			}

			// The legal empty filter proceeds and rides the wire as the
			// official empty object.
			emptyOut := make(chan []ethapi.Receipt, 1)
			var sub ethereum.Subscription
			if view {
				sub, err = client.EthClient().SubscribeTransactionReceipts(ctx, nil, emptyOut)
			} else {
				sub, err = client.SubscribeTransactionReceipts(ctx, nil, emptyOut)
			}
			if err != nil {
				t.Fatalf("legal empty filter rejected: %v", err)
			}
			t.Cleanup(sub.Unsubscribe)
			registered := 0
			for _, peer := range []*subscriptionPeer{a, b} {
				peer.mu.Lock()
				for _, req := range peer.requests {
					if req.Method == "eth_subscribe" {
						registered++
						if len(req.Params) != 2 || string(req.Params[0]) != `"transactionReceipts"` || string(req.Params[1]) != `{}` {
							t.Fatalf("empty filter wire = %s %s", req.Params[0], req.Params[1])
						}
					}
				}
				peer.mu.Unlock()
			}
			if registered != 2 {
				t.Fatalf("registered %d subscriptions, want one per source", registered)
			}
		})
	}
}

// TestSubscribeTransactionReceiptsSingleSourceRejectsInvalidFilter covers the
// Forwarded single-source path: the same input error, zero registration.
func TestSubscribeTransactionReceiptsSingleSourceRejectsInvalidFilter(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	single, err := ethguard.DialUncorroborated(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make(chan []ethapi.Receipt, 1)
	if _, err := single.SubscribeTransactionReceipts(ctx, receiptFilterFixture(t, "0x1"), out); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("single-source invalid filter error = %v, want ErrInvalidRequest", err)
	}
	for _, call := range node.Calls() {
		if call.Method == "eth_subscribe" {
			t.Fatalf("single source registered a subscription: %+v", call)
		}
	}
}

// TestSubscribeTransactionReceiptsCanonicalizesAndFreezesFilter pins the
// legal non-empty path: mixed-case input spellings are canonicalized, the
// frozen copy is immune to later caller mutation, and the registered wire
// carries the canonical lowercase list.
func TestSubscribeTransactionReceiptsCanonicalizesAndFreezesFilter(t *testing.T) {
	a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
	b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
	client := subscriptionClient(t, a, b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	canonical := "0x" + strings.Repeat("42", 32)
	q := receiptFilterFixture(t, "0X"+strings.Repeat("AB", 30)+"4242")
	// Both spellings must canonicalize to the same 32-byte word.
	if q.TransactionHashes == nil || len(*q.TransactionHashes) != 1 {
		t.Fatalf("fixture broken: %v", q)
	}
	(*q.TransactionHashes)[0] = ethapi.Hash("0X" + strings.ToUpper(strings.TrimPrefix(canonical, "0x")))
	out := make(chan []ethapi.Receipt, 1)
	sub, err := client.SubscribeTransactionReceipts(ctx, q, out)
	if err != nil {
		t.Fatalf("legal mixed-case filter rejected: %v", err)
	}
	t.Cleanup(sub.Unsubscribe)
	// A later caller mutation must not change the frozen wire.
	(*q.TransactionHashes)[0] = ethapi.Hash("0x1")
	for _, peer := range []*subscriptionPeer{a, b} {
		peer.mu.Lock()
		saw := false
		for _, req := range peer.requests {
			if req.Method == "eth_subscribe" {
				saw = true
				if len(req.Params) != 2 || string(req.Params[0]) != `"transactionReceipts"` {
					t.Fatalf("registration params = %s", req.Params)
				}
				var wire struct {
					TransactionHashes []string `json:"transactionHashes"`
				}
				if err := json.Unmarshal(req.Params[1], &wire); err != nil {
					t.Fatalf("wire %s: %v", req.Params[1], err)
				}
				if len(wire.TransactionHashes) != 1 || wire.TransactionHashes[0] != canonical {
					t.Fatalf("non-canonical or mutated wire: %s", req.Params[1])
				}
			}
		}
		peer.mu.Unlock()
		if !saw {
			t.Fatalf("peer never saw the registration: %v", peer.requestedMethods())
		}
	}
}

// fixtureSubscriptionLog is an independent canonical logs-subscription event:
// every kernel-required member is present with canonical spellings.
func fixtureSubscriptionLog(blockHash string) json.RawMessage {
	return json.RawMessage(`{"address":"0x` + strings.Repeat("cc", 20) + `",` +
		`"topics":["0x` + strings.Repeat("dd", 32) + `"],` +
		`"data":"0x0102",` +
		`"blockNumber":"0x10",` +
		`"transactionHash":"0x` + strings.Repeat("11", 32) + `",` +
		`"transactionIndex":"0x0",` +
		`"blockHash":"` + blockHash + `",` +
		`"logIndex":"0x0",` +
		`"removed":false}`)
}

// TestSubscribeFilterLogsRangeFilterPullsSameSourceBySeedBlock pins S2 on the
// real WSS path: a legal range (and the default) filter registers its own
// conditions, and when one source misses the pushed event the same-source
// Pull fetches eth_getLogs addressed by the seeded log's block hash — the
// range bounds are replaced, the address filter is preserved, and the
// delivered event is the same log.
func TestSubscribeFilterLogsRangeFilterPullsSameSourceBySeedBlock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"default", `{}`},
		{"range", `{"address":["0x` + strings.Repeat("cc", 20) + `"],"fromBlock":"0x0","toBlock":"latest"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blockHash := "0x" + strings.Repeat("22", 32)
			log := fixtureSubscriptionLog(blockHash)
			a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			b := newSubscriptionPeer(t, map[string][]byte{
				"eth_chainId": []byte(`"0x1"`),
				"eth_getLogs": []byte("[" + string(log) + "]"),
			})
			a.queueNotification(log)
			client := subscriptionClient(t, a, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			q, qErr := ethapi.NewFilterQuery(json.RawMessage(tc.query))
			if qErr != nil {
				t.Fatalf("query %s: %v", tc.query, qErr)
			}
			out := make(chan ethapi.Log, 1)
			sub, subErr := client.SubscribeFilterLogs(ctx, q, out)
			if subErr != nil {
				t.Fatalf("subscribe: %v", subErr)
			}
			t.Cleanup(sub.Unsubscribe)
			select {
			case got := <-out:
				if got.TransactionHash != ethapi.Hash("0x"+strings.Repeat("11", 32)) || got.BlockHash == nil || *got.BlockHash != ethapi.Hash(blockHash) {
					t.Fatalf("delivered log = %+v", got)
				}
			case err := <-sub.Err():
				t.Fatalf("subscription ended before delivery: %v", err)
			case <-ctx.Done():
				t.Fatalf("no delivered log: %v; A=%v B=%v", ctx.Err(), a.requestedMethods(), b.requestedMethods())
			}

			// The registration must carry the subscription's own conditions;
			// the same-source Pull must be hash-addressed by the seeded block
			// with the address filter preserved and no range members.
			a.mu.Lock()
			defer a.mu.Unlock()
			b.mu.Lock()
			defer b.mu.Unlock()
			var registered map[string]json.RawMessage
			for _, req := range a.requests {
				if req.Method == "eth_subscribe" {
					if len(req.Params) != 2 || string(req.Params[0]) != `"logs"` {
						t.Fatalf("registration params = %s", req.Params)
					}
					var arg map[string]json.RawMessage
					if err := json.Unmarshal(req.Params[1], &arg); err != nil {
						t.Fatalf("registration arg %s: %v", req.Params[1], err)
					}
					registered = arg
				}
			}
			if _, ok := registered["fromBlock"]; !ok {
				t.Fatalf("registration lost the range conditions: %s", registered)
			}
			pulled := false
			for _, req := range b.requests {
				if req.Method != "eth_getLogs" {
					continue
				}
				pulled = true
				var arg map[string]json.RawMessage
				if err := json.Unmarshal(req.Params[0], &arg); err != nil {
					t.Fatalf("pull arg %s: %v", req.Params[0], err)
				}
				if _, ok := arg["fromBlock"]; ok {
					t.Fatalf("pull arg carries a range member: %s", req.Params[0])
				}
				if _, ok := arg["toBlock"]; ok {
					t.Fatalf("pull arg carries a range member: %s", req.Params[0])
				}
				if string(arg["blockHash"]) != fmt.Sprintf("%q", blockHash) {
					t.Fatalf("pull arg block hash = %s, want %s", arg["blockHash"], blockHash)
				}
				if tc.query != `{}` {
					if string(arg["address"]) != `["0x`+strings.Repeat("cc", 20)+`"]` {
						t.Fatalf("pull arg lost the address filter: %s", req.Params[0])
					}
				}
			}
			if !pulled {
				t.Fatalf("source b never performed the same-source Pull: %v", b.requestedMethods())
			}
			for _, req := range a.requests {
				if req.Method == "eth_getLogs" {
					t.Fatalf("source a performed a Pull it did not owe: %v", a.requestedMethods())
				}
			}
		})
	}
}

// TestBlockReceiptsRequireCanonicalPresenceOnWire pins S4 on the real HTTP
// wire through both facades: an absent requireCanonical stays absent, an
// explicit false stays false, an explicit true stays true — and the hash
// binding stays intact in all three states.
func TestBlockReceiptsRequireCanonicalPresenceOnWire(t *testing.T) {
	pin := "0x" + strings.Repeat("11", 32)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	cases := []struct {
		name string
		ref  func(t *testing.T) ethapi.BlockReference
		want string
	}{
		{"absent", func(t *testing.T) ethapi.BlockReference {
			return ethapi.BlockReference{Eip1898BlockHashObject: &ethapi.BlockHashReference{BlockHash: ethapi.Hash(pin)}}
		}, `{"blockHash":"0x` + strings.Repeat("11", 32) + `"}`},
		{"explicit false", func(t *testing.T) ethapi.BlockReference {
			falseFlag := false
			return ethapi.BlockReference{Eip1898BlockHashObject: &ethapi.BlockHashReference{BlockHash: ethapi.Hash(pin), RequireCanonical: &falseFlag}}
		}, `{"blockHash":"0x` + strings.Repeat("11", 32) + `","requireCanonical":false}`},
		{"explicit true", func(t *testing.T) ethapi.BlockReference {
			trueFlag := true
			return ethapi.BlockReference{Eip1898BlockHashObject: &ethapi.BlockHashReference{BlockHash: ethapi.Hash(pin), RequireCanonical: &trueFlag}}
		}, `{"blockHash":"0x` + strings.Repeat("11", 32) + `","requireCanonical":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, nodes := fleet(t, 2)
			for _, node := range nodes {
				node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{json.RawMessage(raw)})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			ref := tc.ref(t)
			rootReceipts, _, rootErr := client.BlockReceipts(ctx, ref)
			viewReceipts, viewErr := client.EthClient().BlockReceipts(ctx, ref)
			if rootErr != nil || viewErr != nil || len(rootReceipts) != 1 || len(viewReceipts) != 1 {
				t.Fatalf("receipts root/view = %d/%d, %v/%v", len(rootReceipts), len(viewReceipts), rootErr, viewErr)
			}
			for _, node := range nodes {
				var saw string
				for _, call := range node.Calls() {
					if call.Method != methods.MethodGetBlockReceipts {
						continue
					}
					saw = string(call.Params[0])
					if saw != tc.want {
						t.Fatalf("wire reference = %s, want %s", saw, tc.want)
					}
				}
				if saw == "" {
					t.Fatalf("node never received a receipts request: %v", node.Calls())
				}
			}
		})
	}
}

// TestFilterLogsWireSpellsCanonicalCopy pins S3 on the real HTTP wire through
// both facades: directly constructed mixed-case inputs ride the request as
// the constructor's canonical lowercase copy — for the hash branch, for a
// single address, and for an address array. Open-object extra precision on
// the subscription path is asserted separately by
// TestSubscribeFilterLogsWireSpellsCanonicalCopyForSubscription.
func TestFilterLogsWireSpellsCanonicalCopy(t *testing.T) {
	upperHash := "0X" + strings.ToUpper(strings.Repeat("ab", 32))
	upperAddress := "0X" + strings.ToUpper(strings.Repeat("cc", 20))
	cases := []struct {
		name string
		ref  func(t *testing.T) ethapi.FilterQuery
		want string
	}{
		{"hash branch", func(t *testing.T) ethapi.FilterQuery {
			// The queried block hash: the admission binds every returned
			// log's blockHash to it, so the fixture log must carry it.
			return ethapi.FilterQuery{FilterByBlockHash: &ethapi.FilterQueryHash{BlockHash: ethapi.Hash(upperHash)}}
		}, `{"blockHash":"0x` + strings.Repeat("ab", 32) + `"}`},
		{"single address", func(t *testing.T) ethapi.FilterQuery {
			addr := ethapi.Address(upperAddress)
			return ethapi.FilterQuery{FilterByBlockRange: &ethapi.FilterQueryRange{Address: &ethapi.FilterAddress{Address: &addr}}}
		}, `{"address":"0x` + strings.Repeat("cc", 20) + `","fromBlock":"0x0","toBlock":"0x10"}`},
		{"address array", func(t *testing.T) ethapi.FilterQuery {
			addr := ethapi.Address(upperAddress)
			return ethapi.FilterQuery{FilterByBlockRange: &ethapi.FilterQueryRange{Address: &ethapi.FilterAddress{Addresses: &[]ethapi.Address{addr}}}}
		}, `{"address":["0x` + strings.Repeat("cc", 20) + `"],"fromBlock":"0x0","toBlock":"0x10"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, nodes := fleet(t, 2)
			// The hash branch addresses immutable content directly; the
			// range branch pins the moving toBlock through the anchor round,
			// which every node must answer.
			blockHash := "0x" + strings.Repeat("ab", 32)
			if tc.name != "hash branch" {
				blockHash = "0x" + strings.Repeat("22", 32)
				block := fixtureBlock(t, 16)
				for _, node := range nodes {
					node.Return(methods.MethodGetBlockByNumber, json.RawMessage(block))
				}
			}
			log := fixtureSubscriptionLog(blockHash)
			for _, node := range nodes {
				node.Return(methods.MethodGetLogs, []json.RawMessage{json.RawMessage(log)})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			q := tc.ref(t)
			rootLogs, _, rootErr := client.FilterLogs(ctx, q)
			viewLogs, viewErr := client.EthClient().FilterLogs(ctx, q)
			if rootErr != nil || viewErr != nil || len(rootLogs) != 1 || len(viewLogs) != 1 {
				t.Fatalf("logs root/view = %d/%d, %v/%v", len(rootLogs), len(viewLogs), rootErr, viewErr)
			}
			for _, node := range nodes {
				var saw string
				for _, call := range node.Calls() {
					if call.Method != methods.MethodGetLogs {
						continue
					}
					saw = string(call.Params[0])
					if saw != tc.want {
						t.Fatalf("wire filter = %s, want %s", saw, tc.want)
					}
				}
				if saw == "" {
					t.Fatalf("node never received a getLogs request: %v", node.Calls())
				}
			}
		})
	}
}

// TestFilterLogsMixedBranchIsLocalInputError pins S6's filter half: the
// hash-plus-range combination is an input error (ErrInvalidRequest) rejected
// with zero network contact, and the root completion result carries no
// decision.
func TestFilterLogsMixedBranchIsLocalInputError(t *testing.T) {
	client, nodes := fleet(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q, err := ethapi.NewFilterQuery(json.RawMessage(`{"blockHash":"0x` + strings.Repeat("ab", 32) + `","fromBlock":"0x0"}`))
	if err != nil {
		t.Fatalf("mixed filter must parse: %v", err)
	}
	logs, result, err := client.FilterLogs(ctx, q)
	if !errors.Is(err, ethguard.ErrInvalidRequest) || logs != nil {
		t.Fatalf("mixed filter = %v, %v, want ErrInvalidRequest", logs, err)
	}
	if result.Decision != nil || result.Err != err {
		t.Fatalf("local rejection result = %+v, want nil decision and equal Err", result)
	}
	viewLogs, viewErr := client.EthClient().FilterLogs(ctx, q)
	if !errors.Is(viewErr, ethguard.ErrInvalidRequest) || viewLogs != nil {
		t.Fatalf("view mixed filter = %v, %v", viewLogs, viewErr)
	}
	for _, node := range nodes {
		if calls := node.Calls(); len(calls) != 0 {
			t.Fatalf("mixed filter contacted upstream: %+v", calls)
		}
	}
}

// TestSubscribeFilterLogsWireSpellsCanonicalCopyForSubscription pins S3 on
// the real WSS subscription path through both facades: a directly
// constructed mixed-case filter — including one open-object extra carrying
// an exact integer beyond float64 precision — registers with the
// constructor's canonical lowercase block hash and the extra riding the
// wire byte-exactly.
func TestSubscribeFilterLogsWireSpellsCanonicalCopyForSubscription(t *testing.T) {
	a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
	b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
	client := subscriptionClient(t, a, b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	upperHash := "0X" + strings.ToUpper(strings.Repeat("ab", 32))
	q := ethapi.FilterQuery{FilterByBlockHash: &ethapi.FilterQueryHash{
		BlockHash: ethapi.Hash(upperHash),
		// An open-object extra with an integer a float64 cannot round-trip;
		// the wire must carry the exact literal.
		AdditionalProperties: map[string]json.RawMessage{"customFloor": json.RawMessage(`123456789012345678901234567890`)},
	}}
	for _, view := range []bool{false, true} {
		out := make(chan ethapi.Log, 1)
		var sub ethereum.Subscription
		var err error
		if view {
			sub, err = client.EthClient().SubscribeFilterLogs(ctx, q, out)
		} else {
			sub, err = client.SubscribeFilterLogs(ctx, q, out)
		}
		if err != nil {
			t.Fatalf("view=%t subscribe: %v", view, err)
		}
		t.Cleanup(sub.Unsubscribe)
	}
	for _, peer := range []*subscriptionPeer{a, b} {
		peer.mu.Lock()
		saw := 0
		for _, req := range peer.requests {
			if req.Method != "eth_subscribe" {
				continue
			}
			saw++
			if len(req.Params) != 2 || string(req.Params[0]) != `"logs"` {
				t.Fatalf("registration params = %s", req.Params)
			}
			var arg map[string]json.RawMessage
			if err := json.Unmarshal(req.Params[1], &arg); err != nil {
				t.Fatalf("registration arg %s: %v", req.Params[1], err)
			}
			wantHash := `"0x` + strings.Repeat("ab", 32) + `"`
			if string(arg["blockHash"]) != wantHash {
				t.Fatalf("non-canonical subscription wire blockHash = %s, want %s", arg["blockHash"], wantHash)
			}
			if string(arg["customFloor"]) != `123456789012345678901234567890` {
				t.Fatalf("open extra lost exact precision: %s", arg["customFloor"])
			}
		}
		peer.mu.Unlock()
		if saw != 2 {
			t.Fatalf("expected one registration per facade per peer, saw %d: %v", saw, peer.requestedMethods())
		}
	}
}
