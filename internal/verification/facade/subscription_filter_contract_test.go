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
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
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

func receiptsFacadePolicy() ethguard.ReceiptsSubscriptionPolicy {
	p := logsFacadePolicy()
	return ethguard.ReceiptsSubscriptionPolicy{Head: p.Head, MemoryBytes: p.MemoryBytes, MaxResponseBytes: p.MaxResponseBytes, MaxConcurrentRequests: p.MaxConcurrentRequests, RequestTimeout: p.RequestTimeout, RecoveryTimeout: p.RecoveryTimeout}
}

func TestSubscribeTransactionReceiptsRejectsInvalidFilterBeforeEstablishing(t *testing.T) {
	for _, view := range []bool{false, true} {
		for _, single := range []bool{false, true} {
			t.Run(fmt.Sprintf("view=%t/single=%t", view, single), func(t *testing.T) {
				peers := []*subscriptionPeer{newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})}
				var client *ethguard.Client
				if single {
					var err error
					client, err = ethguard.DialUncorroborated(peers[0].endpoint)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(client.Close)
				} else {
					peers = append(peers, newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)}))
					client = subscriptionClient(t, peers...)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				invalid := receiptFilterFixture(t, "0x"+strings.Repeat("42", 32), "0x1")
				var err error
				if view {
					_, err = client.EthClient().SubscribeTransactionReceipts(ctx, invalid, receiptsFacadePolicy())
				} else {
					_, err = client.SubscribeTransactionReceipts(ctx, invalid, receiptsFacadePolicy())
				}
				if !errors.Is(err, ethguard.ErrInvalidRequest) {
					t.Fatalf("invalid filter: %v", err)
				}
				for _, peer := range peers {
					for _, method := range peer.requestedMethods() {
						if method == "eth_subscribe" {
							t.Fatal("invalid filter reached registration")
						}
					}
				}
			})
		}
	}
}

func TestSubscribeTransactionReceiptsPublicEntrypointsFollowHeadAndFreezeFilter(t *testing.T) {
	for _, view := range []bool{false, true} {
		for _, single := range []bool{false, true} {
			for _, filter := range []string{"nil", "unset", "empty", "match", "zero-match"} {
				t.Run(fmt.Sprintf("view=%t/single=%t/filter=%s", view, single, filter), func(t *testing.T) {
					block, receipts, hash := logsFacadeBlock(t)
					var rows []struct {
						TransactionHash string `json:"transactionHash"`
					}
					if err := json.Unmarshal(receipts, &rows); err != nil {
						t.Fatal(err)
					}
					wantHash := rows[0].TransactionHash
					responses := map[string][]byte{"eth_chainId": []byte(`"0x1"`), "eth_getBlockByHash": block, "eth_getBlockReceipts": receipts}
					peers := []*subscriptionPeer{newSubscriptionPeer(t, responses)}
					peers[0].queueNotification(block)
					var client *ethguard.Client
					if single {
						var err error
						client, err = ethguard.DialUncorroborated(peers[0].endpoint)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(client.Close)
					} else {
						b := newSubscriptionPeer(t, responses)
						b.queueNotification(block)
						peers = append(peers, b)
						client = subscriptionClient(t, peers...)
					}
					var query *ethapi.TransactionReceiptsQuery
					switch filter {
					case "unset":
						query = &ethapi.TransactionReceiptsQuery{}
					case "empty":
						query = receiptFilterFixture(t)
					case "match":
						query = receiptFilterFixture(t, "0X"+strings.ToUpper(strings.TrimPrefix(wantHash, "0x")))
					case "zero-match":
						query = receiptFilterFixture(t, "0x"+strings.Repeat("43", 32))
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					var subscription *ethguard.ReceiptsSubscription
					var err error
					if view {
						subscription, err = client.EthClient().SubscribeTransactionReceipts(ctx, query, receiptsFacadePolicy())
					} else {
						subscription, err = client.SubscribeTransactionReceipts(ctx, query, receiptsFacadePolicy())
					}
					if err != nil {
						t.Fatal(err)
					}
					defer subscription.Close()
					if query != nil && query.TransactionHashes != nil && len(*query.TransactionHashes) != 0 {
						(*query.TransactionHashes)[0] = ethapi.Hash("0x1")
					}
					event, err := subscription.Next(ctx)
					if err != nil || len(event.Path.Appended) != 1 {
						t.Fatalf("event=%+v err=%v", event, err)
					}
					delivery := event.Path.Appended[0]
					if delivery.Node.Hash != hash || event.Path.NewVersion != 1 {
						t.Fatalf("path: %+v", event.Path)
					}
					if filter == "zero-match" {
						if delivery.Receipts == nil || len(delivery.Receipts) != 0 || delivery.Proof != ethguard.ReceiptsTransactionsExcluded || len(delivery.ReceiptSupport.Suppliers) != 0 {
							t.Fatalf("zero match: %+v", delivery)
						}
					} else {
						if len(delivery.Receipts) != 1 || string(delivery.Receipts[0].TransactionHash) != wantHash || delivery.Proof != ethguard.ReceiptsBlockCommitment {
							t.Fatalf("receipts: %+v", delivery)
						}
						wantStatus := rpc.Corroborated
						if single {
							wantStatus = rpc.Forwarded
						}
						if delivery.ReceiptSupport.Status != wantStatus || delivery.ReceiptSupport.Basis != len(peers) || len(delivery.ReceiptSupport.Suppliers) != len(peers) {
							t.Fatalf("support: %+v", delivery.ReceiptSupport)
						}
						if _, err := ethapi.NewReceipt(mustMarshalLogs(t, delivery.Receipts[0])); err != nil {
							t.Fatal(err)
						}
					}
					for _, peer := range peers {
						peer.mu.Lock()
						requests := append([]subscriptionPeerRequest(nil), peer.requests...)
						peer.mu.Unlock()
						registered := false
						for _, request := range requests {
							if request.Method == "eth_subscribe" {
								registered = true
								if len(request.Params) != 1 || string(request.Params[0]) != `"newHeads"` {
									t.Fatalf("registration: %s", request.Params)
								}
							}
							if filter == "zero-match" && request.Method == "eth_getBlockReceipts" {
								t.Fatal("zero-match block fetched receipts")
							}
						}
						if !registered {
							t.Fatal("missing head registration")
						}
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
