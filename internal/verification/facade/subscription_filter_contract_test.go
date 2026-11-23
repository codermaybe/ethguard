package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
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

func TestSubscribeTransactionReceiptsPushHonorsFrozenFilter(t *testing.T) {
	for _, multi := range []bool{false, true} {
		for _, view := range []bool{false, true} {
			for _, matches := range []bool{false, true} {
				t.Run(fmt.Sprintf("multi=%v/view=%v/matches=%v", multi, view, matches), func(t *testing.T) {
					peers := []*subscriptionPeer{newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})}
					if multi {
						peers = append(peers, newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)}))
					}
					var client *ethguard.Client
					if multi {
						client = subscriptionClient(t, peers...)
					} else {
						var err error
						client, err = ethguard.DialUncorroborated(peers[0].endpoint)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(client.Close)
					}
					wanted := "0x" + strings.Repeat("42", 32)
					actual := wanted
					if !matches {
						actual = "0x" + strings.Repeat("43", 32)
					}
					query := receiptFilterFixture(t, wanted)
					out := make(chan []ethapi.Receipt, 1)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					var sub ethereum.Subscription
					var err error
					if view {
						sub, err = client.EthClient().SubscribeTransactionReceipts(ctx, query, out)
					} else {
						sub, err = client.SubscribeTransactionReceipts(ctx, query, out)
					}
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(sub.Unsubscribe)
					// Later input mutation cannot widen or replace the registered filter.
					(*query.TransactionHashes)[0] = ethapi.Hash("0x" + strings.Repeat("43", 32))
					payload, err := json.Marshal([]json.RawMessage{fixtureReceipt(t, ethapi.Hash(actual), 7)})
					if err != nil {
						t.Fatal(err)
					}
					for _, peer := range peers {
						peer.mu.Lock()
						push := peer.push
						peer.mu.Unlock()
						if err := push(payload); err != nil {
							t.Fatal(err)
						}
					}
					select {
					case got := <-out:
						if !matches {
							t.Fatalf("delivered transaction outside frozen filter: %+v", got)
						}
						if len(got) != 1 || string(got[0].TransactionHash) != wanted {
							t.Fatalf("receipt output = %+v", got)
						}
					case err := <-sub.Err():
						if matches || err == nil || !strings.Contains(err.Error(), "filter") {
							t.Fatalf("subscription error = %v", err)
						}
					case <-ctx.Done():
						t.Fatal("subscription produced neither receipt nor filter rejection")
					}
				})
			}
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

func logsFacadePolicy() ethguard.LogsSubscriptionPolicy {
	return ethguard.LogsSubscriptionPolicy{Head: ethguard.HeadSubscriptionPolicy{MemoryBytes: 8 << 20, RetainedNodes: 64, MaxConcurrentRequests: 8, WireMessages: 8, WireBytes: 1 << 16, MaxMessageBytes: 1 << 14}, MemoryBytes: 32 << 20, MaxResponseBytes: 1 << 14, MaxConcurrentRequests: 2, RequestTimeout: time.Second, RecoveryTimeout: time.Second}
}

// Both public entrypoints follow Head even when no provider pushes a log.
// A filter outside the birth height still delivers its empty block progress.
func TestSubscribeFilterLogsPublicEntrypointsDeliverZeroMatchProgress(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			raw := fixtureBlock(t, 17)
			a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			a.queueNotification(raw)
			b.queueNotification(raw)
			client := subscriptionClient(t, a, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			q, err := ethapi.NewFilterQuery(json.RawMessage(`{"fromBlock":"0x20","toBlock":"latest"}`))
			if err != nil {
				t.Fatal(err)
			}
			var sub *ethguard.LogsSubscription
			if view {
				sub, err = client.EthClient().SubscribeFilterLogs(ctx, q, logsFacadePolicy())
			} else {
				sub, err = client.SubscribeFilterLogs(ctx, q, logsFacadePolicy())
			}
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			event, err := sub.Next(ctx)
			if err != nil || len(event.Path.Appended) != 1 || event.Path.Appended[0].Node.Number != 17 || event.Path.Appended[0].Logs == nil || len(event.Path.Appended[0].Logs) != 0 || event.Path.Appended[0].Proof != ethguard.LogsFilterExcluded {
				t.Fatalf("event=%+v err=%v", event, err)
			}
			for _, peer := range []*subscriptionPeer{a, b} {
				peer.mu.Lock()
				requests := append([]subscriptionPeerRequest(nil), peer.requests...)
				peer.mu.Unlock()
				registered := false
				for _, req := range requests {
					if req.Method == "eth_subscribe" {
						registered = true
						if len(req.Params) != 1 || string(req.Params[0]) != `"newHeads"` {
							t.Fatalf("registration=%s", req.Params)
						}
					}
					if req.Method == "eth_getLogs" {
						t.Fatal("legacy seeded log pull survived")
					}
				}
				if !registered {
					t.Fatal("Head subscription absent")
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
// single address, and for an address array. The live subscription freezes
// and applies filters locally; its unknown-extension rejection is covered by
// TestSubscribeFilterLogsRejectsUnsupportedQueriesBeforeNetwork.
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

// Unsupported semantic extensions and finality tags must fail before network
// registration on both public facades.
func TestSubscribeFilterLogsRejectsUnsupportedQueriesBeforeNetwork(t *testing.T) {
	for _, view := range []bool{false, true} {
		for _, raw := range []string{`{"customFloor":123456789012345678901234567890}`, `{"fromBlock":"safe"}`, `{"toBlock":"finalized"}`} {
			t.Run(fmt.Sprintf("%t/%s", view, raw), func(t *testing.T) {
				a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
				b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
				client := subscriptionClient(t, a, b)
				q, err := ethapi.NewFilterQuery(json.RawMessage(raw))
				if err != nil {
					t.Fatal(err)
				}
				var sub *ethguard.LogsSubscription
				if view {
					sub, err = client.EthClient().SubscribeFilterLogs(context.Background(), q, logsFacadePolicy())
				} else {
					sub, err = client.SubscribeFilterLogs(context.Background(), q, logsFacadePolicy())
				}
				if sub != nil {
					defer sub.Close()
				}
				if err == nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
					t.Fatalf("unexpected result %v", err)
				}
				for _, peer := range []*subscriptionPeer{a, b} {
					for _, method := range peer.requestedMethods() {
						if method == "eth_subscribe" {
							t.Fatal("invalid filter reached network")
						}
					}
				}
			})
		}
	}
}

func logsFacadeBlock(t *testing.T) (json.RawMessage, json.RawMessage, common.Hash) {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	address := common.HexToAddress("0x" + strings.Repeat("cc", 20))
	tx, err := types.SignNewTx(key, types.LatestSignerForChainID(big.NewInt(1)), &types.LegacyTx{To: &address, Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(0)})
	if err != nil {
		t.Fatal(err)
	}
	receipt := &types.Receipt{Status: 1, CumulativeGasUsed: 21000, Logs: []*types.Log{{Address: address, Topics: []common.Hash{common.HexToHash("0x1")}, Data: []byte{1}}}, GasUsed: 21000, EffectiveGasPrice: big.NewInt(1), TxHash: tx.Hash(), BlockNumber: big.NewInt(17)}
	receipt.Bloom = types.CreateBloom(receipt)
	head := &types.Header{Number: big.NewInt(17), Difficulty: big.NewInt(0), Time: 123, GasLimit: 30000000, GasUsed: 21000, UncleHash: types.EmptyUncleHash, TxHash: types.DeriveSha(types.Transactions{tx}, trie.NewStackTrie(nil)), ReceiptHash: types.DeriveSha(types.Receipts{receipt}, trie.NewStackTrie(nil)), Bloom: receipt.Bloom}
	receipt.BlockHash = head.Hash()
	receipt.Logs[0].BlockHash = head.Hash()
	receipt.Logs[0].BlockNumber = 17
	receipt.Logs[0].BlockTimestamp = head.Time
	receipt.Logs[0].TxHash = tx.Hash()
	encode := func(value any) json.RawMessage {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var txFields, headFields, receiptFields map[string]json.RawMessage
	_ = json.Unmarshal(encode(tx), &txFields)
	_ = json.Unmarshal(encode(head), &headFields)
	_ = json.Unmarshal(encode(receipt), &receiptFields)
	txFields["from"] = encode(crypto.PubkeyToAddress(key.PublicKey).Hex())
	txFields["blockHash"] = encode(head.Hash().Hex())
	txFields["blockNumber"] = json.RawMessage(`"0x11"`)
	txFields["transactionIndex"] = json.RawMessage(`"0x0"`)
	txFields["blockTimestamp"] = json.RawMessage(`"0x7b"`)
	headFields["hash"] = encode(head.Hash().Hex())
	headFields["transactions"] = encode([]map[string]json.RawMessage{txFields})
	headFields["uncles"] = json.RawMessage(`[]`)
	headFields["size"] = json.RawMessage(`"0x321"`)
	receiptFields["from"] = txFields["from"]
	receiptFields["to"] = encode(address.Hex())
	delete(receiptFields, "root")
	return encode(headFields), encode([]map[string]json.RawMessage{receiptFields}), head.Hash()
}

func TestSubscribeFilterLogsPublicEntrypointsProveNonemptyBlock(t *testing.T) {
	for _, tc := range []struct{ view, single bool }{{}, {view: true}, {single: true}, {view: true, single: true}} {
		t.Run(fmt.Sprintf("view=%t/single=%t", tc.view, tc.single), func(t *testing.T) {
			block, receipts, hash := logsFacadeBlock(t)
			responses := map[string][]byte{"eth_chainId": []byte(`"0x1"`), "eth_getBlockByHash": block, "eth_getBlockReceipts": receipts}
			a := newSubscriptionPeer(t, responses)
			a.queueNotification(block)
			var client *ethguard.Client
			if tc.single {
				var err error
				client, err = ethguard.DialUncorroborated(a.endpoint)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.Close)
			} else {
				b := newSubscriptionPeer(t, responses)
				b.queueNotification(block)
				client = subscriptionClient(t, a, b)
			}
			q, err := ethapi.NewFilterQuery(json.RawMessage(`{"address":"0xcccccccccccccccccccccccccccccccccccccccc"}`))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var subscription *ethguard.LogsSubscription
			if tc.view {
				subscription, err = client.EthClient().SubscribeFilterLogs(ctx, q, logsFacadePolicy())
			} else {
				subscription, err = client.SubscribeFilterLogs(ctx, q, logsFacadePolicy())
			}
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Close()
			basis := subscription.Basis()
			if tc.single && (basis.Tier != ethguard.HeadForwarded || len(basis.Members) != 1 || basis.Quorum != 1) {
				t.Fatalf("single source basis=%+v", basis)
			}
			if !tc.single && (basis.Tier != ethguard.HeadCorroborated || len(basis.Members) != 2 || basis.Quorum != 2) {
				t.Fatalf("multi source basis=%+v", basis)
			}
			event, err := subscription.Next(ctx)
			if err != nil || len(event.Path.Appended) != 1 || len(event.Path.Appended[0].Logs) != 1 {
				t.Fatalf("event=%+v err=%v", event, err)
			}
			entry := event.Path.Appended[0].Logs[0]
			if *entry.BlockHash != ethapi.Hash(hash.Hex()) || *entry.BlockTimestamp != "0x7b" || *entry.Removed || event.Path.Appended[0].Proof != ethguard.LogsBlockCommitment {
				t.Fatalf("log=%+v", entry)
			}
			if _, err := ethapi.NewLog(mustMarshalLogs(t, entry)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func mustMarshalLogs(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
