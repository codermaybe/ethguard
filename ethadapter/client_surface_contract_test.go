package ethadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/rpctest"
	ethereum "github.com/ethereum/go-ethereum"
	bind "github.com/ethereum/go-ethereum/accounts/abi/bind/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

var (
	_ bind.DeployBackend             = (*ethadapter.Client)(nil)
	_ ethereum.ChainStateReader      = (*ethadapter.Client)(nil)
	_ ethereum.ContractCaller        = (*ethadapter.Client)(nil)
	_ ethereum.GasEstimator          = (*ethadapter.Client)(nil)
	_ ethereum.GasPricer             = (*ethadapter.Client)(nil)
	_ ethereum.GasPricer1559         = (*ethadapter.Client)(nil)
	_ ethereum.FeeHistoryReader      = (*ethadapter.Client)(nil)
	_ ethereum.PendingStateReader    = (*ethadapter.Client)(nil)
	_ ethereum.PendingContractCaller = (*ethadapter.Client)(nil)
	_ ethereum.TransactionSender     = (*ethadapter.Client)(nil)
	_ ethereum.ChainIDReader         = (*ethadapter.Client)(nil)
	_ ethereum.ChainSyncReader       = (*ethadapter.Client)(nil)
)

func TestIndependentLockedGethNonSubscriptionMethodSurface(t *testing.T) {
	geth, adapter := reflect.TypeFor[*ethclient.Client](), reflect.TypeFor[*ethadapter.Client]()
	excluded := map[string]bool{"Client": true, "SubscribeNewHead": true, "SubscribeFilterLogs": true, "SubscribeTransactionReceipts": true}
	count := 0
	for i := range geth.NumMethod() {
		want := geth.Method(i)
		if excluded[want.Name] {
			continue
		}
		count++
		got, ok := adapter.MethodByName(want.Name)
		if !ok {
			t.Errorf("missing locked geth method %s", want.Name)
			continue
		}
		if got.Type.NumIn() != want.Type.NumIn() || got.Type.NumOut() != want.Type.NumOut() || got.Type.IsVariadic() != want.Type.IsVariadic() {
			t.Errorf("%s signature: got %s want %s", want.Name, got.Type, want.Type)
			continue
		}
		for input := 1; input < want.Type.NumIn(); input++ {
			if got.Type.In(input) != want.Type.In(input) {
				t.Errorf("%s argument %d: got %s want %s", want.Name, input, got.Type.In(input), want.Type.In(input))
			}
		}
		for output := range want.Type.NumOut() {
			if got.Type.Out(output) != want.Type.Out(output) {
				t.Errorf("%s result %d: got %s want %s", want.Name, output, got.Type.Out(output), want.Type.Out(output))
			}
		}
	}
	if count != 44 {
		t.Fatalf("geth API inventory drifted: got %d non-subscription methods, frozen S5 expects 44", count)
	}
}

func contractInstallBlock(t *testing.T, node *rpctest.Node, head *types.Header, txs []*types.Transaction, uncles []*types.Header) {
	t.Helper()
	headerRaw := contractJSON(t, contractHeaderFields(t, head))
	blockRaw := contractJSON(t, contractBlock(t, head, txs, uncles, nil))
	for _, method := range []string{"eth_getBlockByHash", "eth_getBlockByNumber"} {
		node.Handle(method, func(call rpctest.Call) (any, error) {
			if len(call.Params) == 2 && string(call.Params[1]) == "true" {
				return blockRaw, nil
			}
			return headerRaw, nil
		})
	}
	if len(uncles) == 1 {
		// Node uncle responses have a block envelope; provenance consumption
		// must retain the header while accepting these real RPC body members.
		node.ReturnRaw("eth_getUncleByBlockHashAndIndex", contractJSON(t, contractBlock(t, uncles[0], nil, nil, nil)))
	}
}

func TestIndependentClientRestoresAdmittedUnclesWithoutSourceEvidence(t *testing.T) {
	for _, sourceCount := range []int{1, 2} {
		t.Run(fmt.Sprintf("sources_%d", sourceCount), func(t *testing.T) {
			uncle, head := contractBaseHeader(), contractBaseHeader()
			uncle.Number, uncle.Time = big.NewInt(16), 122
			head.UncleHash = types.CalcUncleHash([]*types.Header{uncle})
			nodes, endpoints := make([]*rpctest.Node, sourceCount), make([]string, sourceCount)
			for i := range nodes {
				nodes[i] = rpctest.NewNode(1)
				t.Cleanup(nodes[i].Close)
				contractInstallBlock(t, nodes[i], head, nil, []*types.Header{uncle})
				endpoints[i] = nodes[i].URL()
			}
			core, err := ethguard.Dial(endpoints...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(core.Close)
			client := ethadapter.New(core)
			for _, byHash := range []bool{false, true} {
				ctx, capture := ethguard.WithResult(t.Context())
				var block *types.Block
				if byHash {
					block, err = client.BlockByHash(ctx, head.Hash())
				} else {
					block, err = client.BlockByNumber(ctx, big.NewInt(17))
				}
				if err != nil {
					t.Fatal(err)
				}
				result, ok, captureErr := capture.Result()
				if captureErr != nil || !ok || result.Err != nil || result.Uncertain != nil || result.Decision == nil || len(result.Decision.Provenance) == 0 {
					t.Fatalf("uncle provenance must be unconditional native result material: %#v %v %v", result, ok, captureErr)
				}
				if block.Hash() != head.Hash() || len(block.Uncles()) != 1 || block.Uncles()[0].Hash() != uncle.Hash() {
					t.Fatal("adapter returned a body missing the admitted uncle")
				}
			}
			for _, node := range nodes {
				uncleCalls := 0
				for _, call := range node.Calls() {
					if call.Method != "eth_getUncleByBlockHashAndIndex" {
						continue
					}
					uncleCalls++
					if len(call.Params) != 2 || string(call.Params[0]) != string(contractJSON(t, head.Hash())) || string(call.Params[1]) != `"0x0"` {
						t.Fatalf("uncle evidence is detached from its block: %#v", call)
					}
				}
				if uncleCalls != 2 {
					t.Fatalf("each admitted block must obtain its own source's uncle evidence, got %d", uncleCalls)
				}
			}
		})
	}
}

func TestIndependentHTTPStateAndContractMethodsUseNativeExecution(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	head := contractBaseHeader()
	contractInstallBlock(t, node, head, nil, nil)
	for method, value := range map[string]string{
		"eth_getBalance": "0x100000000000000000000", "eth_getTransactionCount": "0x2a", "eth_getCode": "0x0102", "eth_getStorageAt": common.HexToHash("0x55").Hex(), "eth_call": "0x0304", "eth_estimateGas": "0x5208", "eth_gasPrice": "0x100000000000000000000", "eth_maxPriorityFeePerGas": "0x100000000000000000000", "eth_blobBaseFee": "0x100000000000000000000", "net_version": "1", "net_peerCount": "0x2a", "eth_getBlockTransactionCountByHash": "0x2a", "eth_getBlockTransactionCountByNumber": "0x2a",
	} {
		node.Return(method, value)
	}
	node.Return("eth_syncing", false)
	client := contractClient(t, node)
	address, key := common.HexToAddress("0x99"), common.HexToHash("0x88")
	msg := ethereum.CallMsg{To: &address, Data: []byte{1}}
	large := new(big.Int).Lsh(big.NewInt(1), 80)
	for _, test := range []struct {
		name   string
		method string
		call   func(context.Context) (any, error)
		want   any
	}{
		{"BalanceAt", "eth_getBalance", func(ctx context.Context) (any, error) { return client.BalanceAt(ctx, address, nil) }, large},
		{"BalanceAtHash", "eth_getBalance", func(ctx context.Context) (any, error) { return client.BalanceAtHash(ctx, address, head.Hash()) }, large},
		{"PendingBalanceAt", "eth_getBalance", func(ctx context.Context) (any, error) { return client.PendingBalanceAt(ctx, address) }, large},
		{"NonceAt", "eth_getTransactionCount", func(ctx context.Context) (any, error) { return client.NonceAt(ctx, address, nil) }, uint64(42)},
		{"NonceAtHash", "eth_getTransactionCount", func(ctx context.Context) (any, error) { return client.NonceAtHash(ctx, address, head.Hash()) }, uint64(42)},
		{"PendingNonceAt", "eth_getTransactionCount", func(ctx context.Context) (any, error) { return client.PendingNonceAt(ctx, address) }, uint64(42)},
		{"CodeAt", "eth_getCode", func(ctx context.Context) (any, error) { return client.CodeAt(ctx, address, nil) }, []byte{1, 2}},
		{"CodeAtHash", "eth_getCode", func(ctx context.Context) (any, error) { return client.CodeAtHash(ctx, address, head.Hash()) }, []byte{1, 2}},
		{"PendingCodeAt", "eth_getCode", func(ctx context.Context) (any, error) { return client.PendingCodeAt(ctx, address) }, []byte{1, 2}},
		{"StorageAt", "eth_getStorageAt", func(ctx context.Context) (any, error) { return client.StorageAt(ctx, address, key, nil) }, common.HexToHash("0x55").Bytes()},
		{"StorageAtHash", "eth_getStorageAt", func(ctx context.Context) (any, error) { return client.StorageAtHash(ctx, address, key, head.Hash()) }, common.HexToHash("0x55").Bytes()},
		{"PendingStorageAt", "eth_getStorageAt", func(ctx context.Context) (any, error) { return client.PendingStorageAt(ctx, address, key) }, common.HexToHash("0x55").Bytes()},
		{"CallContract", "eth_call", func(ctx context.Context) (any, error) { return client.CallContract(ctx, msg, nil) }, []byte{3, 4}},
		{"CallContractAtHash", "eth_call", func(ctx context.Context) (any, error) { return client.CallContractAtHash(ctx, msg, head.Hash()) }, []byte{3, 4}},
		{"PendingCallContract", "eth_call", func(ctx context.Context) (any, error) { return client.PendingCallContract(ctx, msg) }, []byte{3, 4}},
		{"EstimateGas", "eth_estimateGas", func(ctx context.Context) (any, error) { return client.EstimateGas(ctx, msg) }, uint64(21000)},
		{"EstimateGasAtBlock", "eth_estimateGas", func(ctx context.Context) (any, error) { return client.EstimateGasAtBlock(ctx, msg, big.NewInt(17)) }, uint64(21000)},
		{"EstimateGasAtBlockHash", "eth_estimateGas", func(ctx context.Context) (any, error) { return client.EstimateGasAtBlockHash(ctx, msg, head.Hash()) }, uint64(21000)},
		{"SuggestGasPrice", "eth_gasPrice", func(ctx context.Context) (any, error) { return client.SuggestGasPrice(ctx) }, large},
		{"SuggestGasTipCap", "eth_maxPriorityFeePerGas", func(ctx context.Context) (any, error) { return client.SuggestGasTipCap(ctx) }, large},
		{"BlobBaseFee", "eth_blobBaseFee", func(ctx context.Context) (any, error) { return client.BlobBaseFee(ctx) }, large},
		{"NetworkID", "net_version", func(ctx context.Context) (any, error) { return client.NetworkID(ctx) }, big.NewInt(1)},
		{"PeerCount", "net_peerCount", func(ctx context.Context) (any, error) { return client.PeerCount(ctx) }, uint64(42)},
		{"TransactionCount", "eth_getBlockTransactionCountByHash", func(ctx context.Context) (any, error) { return client.TransactionCount(ctx, head.Hash()) }, uint(42)},
		{"PendingTransactionCount", "eth_getBlockTransactionCountByNumber", func(ctx context.Context) (any, error) { return client.PendingTransactionCount(ctx) }, uint(42)},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := len(node.Calls())
			ctx, capture := ethguard.WithResult(t.Context())
			got, err := test.call(ctx)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, test.want)
			}
			result, published, captureErr := capture.Result()
			if captureErr != nil || !published || result.Err != nil || result.Decision == nil {
				t.Fatalf("adapter bypassed/obscured native execution: %#v %v %v", result, published, captureErr)
			}
			calls := 0
			for _, call := range node.Calls()[before:] {
				if call.Method == test.method {
					calls++
					if strings.HasPrefix(test.name, "Pending") {
						if len(call.Params) == 0 || string(call.Params[len(call.Params)-1]) != `"pending"` {
							t.Fatalf("pending operation was rewritten: %#v", call)
						}
					}
				}
			}
			if calls != 1 {
				t.Fatalf("business request count = %d, want 1", calls)
			}
		})
	}
	if client.Core() == nil {
		t.Fatal("Core accessor lost shared execution owner")
	}
}

func TestIndependentHTTPFilterDefaultsReachWire(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	contractInstallBlock(t, node, contractBaseHeader(), nil, nil)
	node.Return("eth_getLogs", []any{})
	client := contractClient(t, node)
	logs, err := client.FilterLogs(t.Context(), ethereum.FilterQuery{})
	if err != nil || len(logs) != 0 {
		t.Fatalf("default filter failed: %v %v", logs, err)
	}
	for _, call := range node.Calls() {
		if call.Method != "eth_getLogs" {
			continue
		}
		var fields map[string]json.RawMessage
		if len(call.Params) != 1 || json.Unmarshal(call.Params[0], &fields) != nil {
			t.Fatalf("invalid filter request: %#v", call)
		}
		if string(fields["fromBlock"]) != `"0x0"` || string(fields["toBlock"]) != `"0x11"` || fields["address"] != nil || fields["topics"] != nil {
			t.Fatalf("geth default range/presence changed before native pinned execution: %s", call.Params[0])
		}
		return
	}
	t.Fatal("no filter request reached HTTP")
}

func TestIndependentBindDeployBackendConsumesHTTPAdapter(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	head := contractBaseHeader()
	contractInstallBlock(t, node, head, nil, nil)
	txHash, address := common.HexToHash("0xab"), common.HexToAddress("0xcd")
	receipt := &types.Receipt{Status: 1, CumulativeGasUsed: 21000, GasUsed: 21000, TxHash: txHash, BlockHash: head.Hash(), BlockNumber: head.Number, ContractAddress: address, EffectiveGasPrice: big.NewInt(1), Logs: []*types.Log{}}
	node.ReturnRaw("eth_getTransactionReceipt", contractJSON(t, contractReceipt(t, receipt)))
	node.Return("eth_getCode", "0x6000")
	client := contractClient(t, node)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	got, err := bind.WaitDeployed(ctx, client, txHash)
	if err != nil || got != address {
		t.Fatalf("bind DeployBackend consumption failed: address=%s err=%v", got, err)
	}
	node.Return("eth_getTransactionReceipt", nil)
	_, err = client.TransactionReceipt(t.Context(), txHash)
	if !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("adapter changed geth-compatible absence error: %v", err)
	}
}

func TestIndependentDialContextIsLazyAndDoesNotRetainConstructionContext(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	ctx, cancel := context.WithCancel(t.Context())
	client, err := ethadapter.DialContext(ctx, node.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if len(node.Calls()) != 0 {
		t.Fatal("construction eagerly contacted upstream")
	}
	cancel()
	id, err := client.ChainID(t.Context())
	if err != nil || id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("construction context became client lifetime: %v %v", id, err)
	}
	if !bytes.Equal(contractJSON(t, id), []byte("1")) {
		t.Fatal("unexpected chain identity")
	}
	client.Close()
	if _, _, err := client.Core().ChainID(t.Context()); err == nil {
		t.Fatal("adapter Close did not close shared core")
	}
}

func TestIndependentHTTPTransactionContextAndSenderForEverySignedType(t *testing.T) {
	for _, tx := range contractSignedTransactions(t) {
		t.Run(fmt.Sprintf("type_%d", tx.Type()), func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			block := common.HexToHash("0xab")
			fields := contractTransactionFields(t, tx)
			node.ReturnRaw("eth_getTransactionByHash", contractJSON(t, fields))
			node.ReturnRaw("eth_getTransactionByBlockHashAndIndex", contractJSON(t, fields))
			var last ethadapter.AuditEvent
			client := contractClient(t, node, ethadapter.WithAudit(func(_ context.Context, event ethadapter.AuditEvent) { last = event }))
			got, pending, err := client.TransactionByHash(t.Context(), tx.Hash())
			if err != nil || pending || got.Hash() != tx.Hash() || len(last.ConversionReport.Omissions) == 0 {
				t.Fatalf("mined transaction metadata/hash/audit failed: tx=%v pending=%v err=%v audit=%#v", got, pending, err, last)
			}
			got, err = client.TransactionInBlock(t.Context(), block, 0)
			if err != nil || got.Hash() != tx.Hash() {
				t.Fatalf("transaction in block: %v %v", got, err)
			}
			sender, err := client.TransactionSender(t.Context(), tx, block, 0)
			if err != nil || sender != common.HexToAddress("0x1a642f0E3c3aF545E7AcBD38b07251B3990914F1") {
				t.Fatalf("sender lookup: %s %v", sender, err)
			}
			for _, field := range []string{"blockHash", "blockNumber", "transactionIndex", "blockTimestamp"} {
				fields[field] = json.RawMessage("null")
			}
			node.ReturnRaw("eth_getTransactionByHash", contractJSON(t, fields))
			got, pending, err = client.TransactionByHash(t.Context(), tx.Hash())
			if err != nil || !pending || got.Hash() != tx.Hash() {
				t.Fatalf("pending transaction lost signed payload/pending context: tx=%v pending=%v err=%v", got, pending, err)
			}
		})
	}
}

func TestIndependentHTTPFeeHistoryAndSyncProgressConversion(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	node.ReturnRaw("eth_feeHistory", json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x100000000000000000000","0x2","0x3"],"gasUsedRatio":[0.25,0.75],"reward":[["0x4"],["0x100000000000000000000"]],"baseFeePerBlobGas":["0x5","0x6","0x7"],"blobGasUsedRatio":[0,0.5]}`))
	node.Return("eth_syncing", false)
	var last ethadapter.AuditEvent
	client := contractClient(t, node, ethadapter.WithAudit(func(_ context.Context, event ethadapter.AuditEvent) { last = event }))
	history, err := client.FeeHistory(t.Context(), 2, big.NewInt(17), []float64{50})
	if err != nil {
		t.Fatal(err)
	}
	large := new(big.Int).Lsh(big.NewInt(1), 80)
	if history.OldestBlock.Cmp(big.NewInt(16)) != 0 || len(history.BaseFee) != 3 || history.BaseFee[0].Cmp(large) != 0 || len(history.Reward) != 2 || history.Reward[1][0].Cmp(large) != 0 || !reflect.DeepEqual(history.GasUsedRatio, []float64{0.25, 0.75}) {
		t.Fatalf("fee history narrowed or reordered values: %#v", history)
	}
	if len(last.ConversionReport.Omissions) < 2 {
		t.Fatal("blob fee and ratio omission was not audited")
	}
	for _, call := range node.Calls() {
		if call.Method == "eth_feeHistory" && (len(call.Params) != 3 || string(call.Params[0]) != `"0x2"` || string(call.Params[1]) != `"0x11"` || string(call.Params[2]) != "[50]") {
			t.Fatalf("fee history request changed: %#v", call)
		}
	}
	progress, err := client.SyncProgress(t.Context())
	if err != nil || progress != nil {
		t.Fatalf("false syncing must project to nil: %#v %v", progress, err)
	}
	node.ReturnRaw("eth_syncing", json.RawMessage(`{"startingBlock":"0x1","currentBlock":"0x2","highestBlock":"0x3"}`))
	progress, err = client.SyncProgress(t.Context())
	if err != nil || progress.StartingBlock != 1 || progress.CurrentBlock != 2 || progress.HighestBlock != 3 || len(last.ConversionReport.Omissions) == 0 {
		t.Fatalf("sync counters/presence report lost: %#v %v %#v", progress, err, last)
	}
}

func TestIndependentFeeHistoryTypedEntriesShareTagAndWireContract(t *testing.T) {
	for _, entry := range []string{"root", "view", "adapter"} {
		for _, test := range []struct {
			name   string
			number *big.Int
			tag    string
		}{
			{"default", nil, "latest"},
			{"latest", big.NewInt(-2), "latest"},
			{"safe", big.NewInt(-4), "safe"},
			{"finalized", big.NewInt(-3), "finalized"},
			{"pending", big.NewInt(-1), "pending"},
			{"invalid_negative", big.NewInt(-99), ""},
		} {
			t.Run(entry+"/"+test.name, func(t *testing.T) {
				node := rpctest.NewNode(1)
				t.Cleanup(node.Close)
				contractInstallBlock(t, node, contractBaseHeader(), nil, nil)
				node.ReturnRaw("eth_feeHistory", json.RawMessage(`{"oldestBlock":"0x11","baseFeePerGas":["0x2","0x3"],"gasUsedRatio":[0.25]}`))
				client := contractClient(t, node)
				ctx, capture := ethguard.WithResult(t.Context())
				var err error
				switch entry {
				case "root":
					_, _, err = client.Core().FeeHistory(ctx, 1, test.number, nil)
				case "view":
					_, err = client.Core().EthClient().FeeHistory(ctx, 1, test.number, nil)
				case "adapter":
					_, err = client.FeeHistory(ctx, 1, test.number, nil)
				}
				result, ok, captureErr := capture.Result()
				if captureErr != nil || !ok {
					t.Fatalf("typed entry lost its outer capture: %#v %v %v", result, ok, captureErr)
				}
				if test.tag == "" {
					if !errors.Is(err, ethguard.ErrInvalidRequest) || !errors.Is(result.Err, ethguard.ErrInvalidRequest) || len(node.Calls()) != 0 {
						t.Fatalf("unknown negative tag must fail locally with native capture: %v %#v calls=%d", err, result, len(node.Calls()))
					}
					return
				}
				if err != nil || result.Err != nil {
					t.Fatalf("valid tag failed: %v %#v", err, result)
				}
				anchors, businesses := 0, 0
				for _, call := range node.Calls() {
					switch call.Method {
					case "eth_getBlockByNumber":
						anchors++
						if len(call.Params) == 0 || string(call.Params[0]) != string(contractJSON(t, test.tag)) {
							t.Fatalf("tag reference changed: %#v", call)
						}
					case "eth_feeHistory":
						businesses++
						wantLast := `"0x11"`
						if test.tag == "pending" {
							wantLast = `"pending"`
						}
						if len(call.Params) != 3 || string(call.Params[0]) != `"0x1"` || string(call.Params[1]) != wantLast {
							t.Fatalf("typed quantity/tag encoding mismatch: %#v", call)
						}
					}
				}
				wantAnchors := 1
				if test.tag == "pending" {
					wantAnchors = 0
				}
				if anchors != wantAnchors || businesses != 1 {
					t.Fatalf("tag execution path anchors=%d business=%d", anchors, businesses)
				}
			})
		}
	}
}

func TestIndependentHTTPBlockReceiptsReferenceForms(t *testing.T) {
	head := contractBaseHeader()
	for _, test := range []struct {
		name      string
		ref       gethrpc.BlockNumberOrHash
		canonical bool
	}{
		{"number", gethrpc.BlockNumberOrHashWithNumber(17), false},
		{"hash", gethrpc.BlockNumberOrHashWithHash(head.Hash(), false), false},
		{"canonical_hash", gethrpc.BlockNumberOrHashWithHash(head.Hash(), true), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			contractInstallBlock(t, node, head, nil, nil)
			receipt := &types.Receipt{Status: 1, CumulativeGasUsed: 21000, GasUsed: 21000, TxHash: common.HexToHash("0xab"), BlockHash: head.Hash(), BlockNumber: head.Number, EffectiveGasPrice: big.NewInt(1), Logs: []*types.Log{}}
			node.Return("eth_getBlockReceipts", []any{contractReceipt(t, receipt)})
			client := contractClient(t, node)
			ctx, capture := ethguard.WithResult(t.Context())
			rows, err := client.BlockReceipts(ctx, test.ref)
			if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], receipt) {
				t.Fatalf("block receipts reference: %#v %v", rows, err)
			}
			result, ok, captureErr := capture.Result()
			if captureErr != nil || !ok || result.Decision == nil || result.Err != nil {
				t.Fatalf("native receipt decision lost: %#v %v %v", result, ok, captureErr)
			}
			for _, call := range node.Calls() {
				if call.Method != "eth_getBlockReceipts" {
					continue
				}
				if len(call.Params) != 1 {
					t.Fatalf("wrong receipt params: %#v", call)
				}
				if test.ref.BlockHash != nil {
					var fields struct {
						BlockHash        common.Hash
						RequireCanonical bool
					}
					if err := json.Unmarshal(call.Params[0], &fields); err != nil || fields.BlockHash != head.Hash() || fields.RequireCanonical != test.canonical {
						t.Fatalf("receipt hash reference/canonicality changed: %s %v", call.Params[0], err)
					}
				} else if string(call.Params[0]) != `"0x11"` {
					t.Fatalf("receipt number changed: %s", call.Params[0])
				}
				return
			}
			t.Fatal("receipt request did not reach node")
		})
	}
}

func TestIndependentSynchronousWritesKeepSingleBroadcastAndReceiptEvidence(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw_%v", raw), func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			tx := contractSignedTransactions(t)[0]
			head := contractBaseHeader()
			receipt := &types.Receipt{Status: 1, CumulativeGasUsed: 21000, GasUsed: 21000, TxHash: tx.Hash(), BlockHash: head.Hash(), BlockNumber: head.Number, EffectiveGasPrice: big.NewInt(1), Logs: []*types.Log{}}
			node.Return("eth_sendRawTransaction", tx.Hash().Hex())
			node.ReturnRaw("eth_getTransactionReceipt", contractJSON(t, contractReceipt(t, receipt)))
			observed := 0
			client := contractClient(t, node, ethadapter.WithAudit(func(context.Context, ethadapter.AuditEvent) { observed++; panic("sink failed") }))
			ctx, capture := ethguard.WithResult(t.Context())
			timeout := 5 * time.Second
			var got *types.Receipt
			var err error
			if raw {
				payload, marshalErr := tx.MarshalBinary()
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				got, err = client.SendRawTransactionSync(ctx, payload, &timeout)
			} else {
				got, err = client.SendTransactionSync(ctx, tx, &timeout)
			}
			if err != nil || got.TxHash != tx.Hash() || got.Status != 1 || observed != 1 {
				t.Fatalf("synchronous write changed by observer: %#v %v observed=%d", got, err, observed)
			}
			result, ok, captureErr := capture.Result()
			if captureErr != nil || !ok || result.WriteDecision == nil || result.ReceiptWait == nil || result.ReceiptWait.LastObservation == nil || result.Err != nil {
				t.Fatalf("broadcast and receipt evidence must remain separate: %#v %v %v", result, ok, captureErr)
			}
			writes := 0
			for _, call := range node.Calls() {
				if call.Method == "eth_sendRawTransaction" {
					writes++
				}
				if call.Method == "eth_sendRawTransactionSync" {
					t.Fatal("adapter bypassed native broadcast-and-wait ownership")
				}
			}
			if writes != 1 {
				t.Fatalf("successful synchronous send broadcast %d times", writes)
			}
		})
	}
}

func contractOfficialResult(t *testing.T, path string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("../internal/spec/vectors/" + path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "<< ") {
			continue
		}
		var response struct{ Result json.RawMessage }
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "<< ")), &response); err != nil {
			t.Fatal(err)
		}
		return response.Result
	}
	t.Fatal("official vector has no result")
	return nil
}

func TestIndependentSimulateHTTPPreservesSuccessAndInnerFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		vector  string
		omitMax bool
	}{
		{"success", "ethSimulate-simple.io", false},
		{"success_without_maximum", "ethSimulate-simple.io", true},
		{"inner_revert", "ethSimulate-eth-send-should-not-produce-logs-on-revert.io", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			node.ReturnRaw("eth_getBlockByNumber", contractOfficialResult(t, "eth_getBlockByNumber/get-latest.io"))
			raw := contractOfficialResult(t, "eth_simulateV1/"+test.vector)
			if test.omitMax {
				var rows []map[string]json.RawMessage
				if err := json.Unmarshal(raw, &rows); err != nil {
					t.Fatal(err)
				}
				for i := range rows {
					var calls []map[string]json.RawMessage
					if err := json.Unmarshal(rows[i]["calls"], &calls); err != nil {
						t.Fatal(err)
					}
					for j := range calls {
						delete(calls[j], "maxUsedGas")
						calls[j]["returnData"] = json.RawMessage(`"0x0102"`)
					}
					rows[i]["calls"] = contractJSON(t, calls)
				}
				raw = contractJSON(t, rows)
			}
			var expected []ethclient.SimulateBlockResult
			if err := json.Unmarshal(raw, &expected); err != nil {
				t.Fatal(err)
			}
			// The native projection intentionally retains standard error code and
			// message; provider-specific error.data remains raw source evidence.
			for i := range expected {
				for j := range expected[i].Calls {
					if expected[i].Calls[j].Error != nil {
						expected[i].Calls[j].Error.Data = ""
						expected[i].Calls[j].Logs = nil
					}
				}
			}
			node.ReturnRaw("eth_simulateV1", raw)
			var last ethadapter.AuditEvent
			client := contractClient(t, node, ethadapter.WithAudit(func(_ context.Context, event ethadapter.AuditEvent) { last = event }))
			calls := make([]ethereum.CallMsg, len(expected[0].Calls))
			opts := ethclient.SimulateOptions{BlockStateCalls: []ethclient.SimulateBlock{{Calls: calls}}}
			ctx, capture := ethguard.WithResult(t.Context())
			got, err := client.SimulateV1(ctx, opts, nil)
			if err != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("simulated result differs from locked geth projection: got=%#v want=%#v err=%v", got, expected, err)
			}
			result, ok, captureErr := capture.Result()
			if captureErr != nil || !ok || result.Err != nil || result.Decision == nil || last.AdapterErr != nil || len(last.ConversionReport.Omissions) == 0 {
				t.Fatalf("simulation inner result became a top-level failure or omissions disappeared: %#v %#v %v %v", result, last, ok, captureErr)
			}
			if test.omitMax {
				reported := false
				for _, omission := range last.ConversionReport.Omissions {
					if omission.Field == "calls.maxUsedGas" {
						reported = true
					}
				}
				if !reported {
					t.Fatal("missing maxUsedGas was silently converted to zero")
				}
			}
		})
	}
}
