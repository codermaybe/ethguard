//go:build anvil

package anvilverification_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/internal/testkit/anvil"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// This is an opt-in executable contract, not provider evidence. Each client has
// exactly one source. HTTP and raw WSS are separate consumer paths to that SAME
// local process; they are never configured as independent voting members.
//
// ANVIL_BIN selects the required 1.7.1 binary. ETHGUARD_ANVIL_ARTIFACT_DIR, when
// set, must name a new directory; it retains original bounded oracle exchanges,
// single-source production decisions/raw evidence, identity, and recovery limits.
func TestTransactionLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	evidence := newEvidence(t)
	var nodes []*anvil.Node
	t.Cleanup(func() {
		for _, node := range nodes {
			if err := node.Close(); err != nil {
				t.Errorf("Anvil cleanup: %v", err)
			}
			evidence.Sessions = append(evidence.Sessions, session{node.Provenance, node.Logs(), node.Records()})
		}
		evidence.Failed = t.Failed()
		evidence.persist(t)
	})
	start := func() *anvil.Node {
		node, err := anvil.Start(ctx)
		if node != nil {
			nodes = append(nodes, node)
		}
		if err != nil {
			t.Fatalf("required local Anvil: %v", err)
		}
		t.Logf("Anvil %s commit=%s executable=%s sha256=%s", anvil.Version, anvil.Commit, node.Provenance.Executable, node.Provenance.SHA256)
		return node
	}
	node := start()
	oracle := &stateOracle{t: t, ctx: ctx, node: node, evidence: evidence}
	genesis := oracle.block("eth_getBlockByNumber", "0x0", true)
	if genesis.Number != 0 || genesis.Timestamp != anvil.GenesisTime || genesis.GasLimit != anvil.GasLimit || genesis.BaseFee == nil || genesis.BaseFee.ToInt().Cmp(big.NewInt(anvil.BaseFee)) != 0 || len(genesis.Transactions) != 0 {
		t.Fatalf("unpinned genesis: %+v", genesis)
	}
	initial, ok := new(big.Int).SetString(anvil.InitialBalance, 10)
	if !ok {
		t.Fatal("invalid pinned balance")
	}
	oracle.account("0x0", initial, initial, 0)
	consumers := openConsumers(t, node)
	defer func() {
		for _, c := range consumers {
			c.root.Close()
		}
	}()
	for _, c := range consumers {
		c.account(t, ctx, evidence, "genesis", big.NewInt(0), initial, 0)
	}

	var snapshot string
	oracle.call(&snapshot, "evm_snapshot")
	if snapshot == "" {
		t.Fatal("Anvil returned an empty snapshot handle")
	}
	key, err := crypto.HexToECDSA(anvil.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(key.PublicKey) != common.HexToAddress(anvil.Sender) {
		t.Fatal("dev signing key differs from funded sender")
	}
	to := common.HexToAddress(anvil.Recipient)
	tx, err := types.SignTx(types.NewTx(&types.LegacyTx{Nonce: 0, To: &to, Value: big.NewInt(1), Gas: 21000, GasPrice: big.NewInt(anvil.GasPrice)}), types.NewEIP155Signer(big.NewInt(anvil.ChainID)), key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var accepted common.Hash
	oracle.call(&accepted, "eth_sendRawTransaction", hexutil.Encode(encoded))
	if accepted != tx.Hash() {
		t.Fatalf("submitted hash %s != signed hash %s", accepted, tx.Hash())
	}
	pending := oracle.raw("eth_getTransactionByHash", tx.Hash())
	checkTransaction(t, pending, tx, nil)
	oracle.absent("eth_getTransactionReceipt", tx.Hash())
	oracle.quantity("eth_blockNumber", 0)
	for _, c := range consumers {
		c.transaction(t, ctx, evidence, "pending", tx, true)
		c.receipt(t, ctx, evidence, "pending", tx.Hash(), nil)
	}

	var mined any
	oracle.call(&mined, "evm_mine", anvil.GenesisTime+1)
	oracle.quantity("eth_blockNumber", 1)
	block := oracle.block("eth_getBlockByNumber", "0x1", true)
	if block.Number != 1 || block.Timestamp != anvil.GenesisTime+1 || block.ParentHash != genesis.Hash || len(block.Transactions) != 1 {
		t.Fatalf("unexpected mined block: %+v", block)
	}
	checkTransaction(t, block.Transactions[0], tx, &block)
	byHash := oracle.raw("eth_getTransactionByHash", tx.Hash())
	byIndex := oracle.raw("eth_getTransactionByBlockHashAndIndex", block.Hash, "0x0")
	checkTransaction(t, byHash, tx, &block)
	checkTransaction(t, byIndex, tx, &block)
	if !jsonEqual(byHash, byIndex) {
		t.Fatal("by-hash and by-block-index differ")
	}
	receiptRaw := oracle.raw("eth_getTransactionReceipt", tx.Hash())
	var receipt *types.Receipt
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil || receipt == nil {
		t.Fatalf("receipt: %s, %v", receiptRaw, err)
	}
	if receipt.TxHash != tx.Hash() || receipt.BlockHash != block.Hash || receipt.BlockNumber == nil || receipt.BlockNumber.Uint64() != 1 || receipt.TransactionIndex != 0 || receipt.Status != types.ReceiptStatusSuccessful || receipt.GasUsed != 21000 || receipt.CumulativeGasUsed != 21000 || receipt.EffectiveGasPrice == nil || receipt.EffectiveGasPrice.Cmp(tx.GasPrice()) != 0 || len(receipt.Logs) != 0 {
		t.Fatalf("receipt/inclusion disagree: %+v", receipt)
	}
	spent := new(big.Int).Add(new(big.Int).Mul(big.NewInt(21000), tx.GasPrice()), big.NewInt(1))
	balance := new(big.Int).Sub(new(big.Int).Set(initial), spent)
	recipientBalance := new(big.Int).Add(new(big.Int).Set(initial), big.NewInt(1))
	oracle.account("0x1", balance, recipientBalance, 1)
	for _, c := range consumers {
		c.included(t, ctx, evidence, "mined", tx, block, receipt)
		c.account(t, ctx, evidence, "mined", big.NewInt(1), balance, 1)
	}

	oracle.call(&mined, "evm_mine", anvil.GenesisTime+2)
	oracle.quantity("eth_blockNumber", 2)
	// Frozen, historical objects must survive a changed head byte-for-byte as
	// JSON values; no moving tag is substituted for their fixed hash/height.
	for _, query := range []struct {
		method string
		params []any
		want   json.RawMessage
	}{
		{"eth_getTransactionByHash", []any{tx.Hash()}, byHash},
		{"eth_getTransactionByBlockHashAndIndex", []any{block.Hash, "0x0"}, byIndex},
		{"eth_getTransactionReceipt", []any{tx.Hash()}, receiptRaw},
		{"eth_getBlockByNumber", []any{"0x1", true}, block.raw},
		{"eth_getBlockByHash", []any{block.Hash, true}, block.raw},
	} {
		if got := oracle.raw(query.method, query.params...); !jsonEqual(got, query.want) {
			t.Fatalf("historical %s changed after advance", query.method)
		}
	}
	oracle.account("0x1", balance, recipientBalance, 1)
	for _, c := range consumers {
		c.included(t, ctx, evidence, "historical-after-advance", tx, block, receipt)
	}

	var reverted bool
	oracle.call(&reverted, "evm_revert", snapshot)
	if !reverted {
		t.Fatal("snapshot revert was rejected")
	}
	oracle.quantity("eth_blockNumber", 0)
	oracle.account("latest", initial, initial, 0)
	if restored := oracle.block("eth_getBlockByNumber", "0x0", true); !jsonEqual(restored.raw, genesis.raw) {
		t.Fatal("genesis changed after revert")
	}
	oracle.absent("eth_getBlockByNumber", "0x1", true)
	// Snapshot recovery may leave pool/history caches. Identify precisely what
	// survived; only exact prior objects or the same signed pending transaction
	// are accepted here. This is NOT a claim of complete isolation.
	recoveredTx := oracle.raw("eth_getTransactionByHash", tx.Hash())
	switch {
	case isNull(recoveredTx):
		evidence.Recovery.Transaction = "absent"
	case jsonEqual(recoveredTx, byHash):
		evidence.Recovery.Transaction = "retained with old inclusion (partial recovery)"
	default:
		checkTransaction(t, recoveredTx, tx, nil)
		evidence.Recovery.Transaction = "retained in pending pool (partial recovery)"
	}
	for _, query := range []struct {
		method   string
		params   []any
		prior    json.RawMessage
		retained *bool
	}{
		{"eth_getTransactionReceipt", []any{tx.Hash()}, receiptRaw, &evidence.Recovery.ReceiptRetained},
		{"eth_getBlockByHash", []any{block.Hash, true}, block.raw, &evidence.Recovery.BlockByHashRetained},
	} {
		got := oracle.raw(query.method, query.params...)
		*query.retained = !isNull(got)
		if !isNull(got) && !jsonEqual(got, query.prior) {
			t.Fatalf("revert retained changed %s object", query.method)
		}
	}
	var poolNonce hexutil.Uint64
	oracle.call(&poolNonce, "eth_getTransactionCount", anvil.Sender, "pending")
	if poolNonce > 1 {
		t.Fatalf("unexpected pending nonce after revert: %d", poolNonce)
	}
	evidence.Recovery.PendingNonce = uint64(poolNonce)
	t.Logf("snapshot recovery: %+v; restarting pinned genesis for isolation", evidence.Recovery)
	for _, c := range consumers {
		c.account(t, ctx, evidence, "reverted", big.NewInt(0), initial, 0)
		if isNull(recoveredTx) {
			c.transactionAbsent(t, ctx, evidence, "reverted", tx.Hash())
		} else {
			c.transaction(t, ctx, evidence, "reverted", tx, !jsonEqual(recoveredTx, byHash))
		}
		var recoveredReceipt *types.Receipt
		if evidence.Recovery.ReceiptRetained {
			recoveredReceipt = receipt
		}
		c.receipt(t, ctx, evidence, "reverted", tx.Hash(), recoveredReceipt)
	}
	for _, c := range consumers {
		c.root.Close()
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	// A fresh process is the isolation boundary even when this particular
	// snapshot appears complete. No pool/cache reset is silently assumed.
	fresh := start()
	freshOracle := &stateOracle{t: t, ctx: ctx, node: fresh, evidence: evidence}
	freshOracle.quantity("eth_blockNumber", 0)
	freshOracle.account("latest", initial, initial, 0)
	if got := freshOracle.block("eth_getBlockByNumber", "0x0", true); got.Hash != genesis.Hash {
		t.Fatalf("restart genesis hash %s != %s", got.Hash, genesis.Hash)
	}
	freshOracle.absent("eth_getTransactionByHash", tx.Hash())
	freshOracle.absent("eth_getTransactionReceipt", tx.Hash())
	freshOracle.absent("eth_getBlockByHash", block.Hash, true)
	freshOracle.absent("eth_getBlockByNumber", "0x1", true)
	for _, c := range openConsumers(t, fresh) {
		c.transactionAbsent(t, ctx, evidence, "restart", tx.Hash())
		c.receipt(t, ctx, evidence, "restart", tx.Hash(), nil)
	}
	evidence.Recovery.RestartIsolated = true
}

type wireBlock struct {
	Hash         common.Hash       `json:"hash"`
	ParentHash   common.Hash       `json:"parentHash"`
	Number       hexutil.Uint64    `json:"number"`
	Timestamp    hexutil.Uint64    `json:"timestamp"`
	GasLimit     hexutil.Uint64    `json:"gasLimit"`
	BaseFee      *hexutil.Big      `json:"baseFeePerGas"`
	Transactions []json.RawMessage `json:"transactions"`
	raw          json.RawMessage
}

type stateOracle struct {
	t        *testing.T
	ctx      context.Context
	node     *anvil.Node
	evidence *runEvidence
}

func (o *stateOracle) call(result any, method string, params ...any) {
	o.t.Helper()
	if err := o.node.Call(o.ctx, result, method, params...); err != nil {
		o.t.Fatalf("direct oracle/control %s: %v", method, err)
	}
}

func (o *stateOracle) raw(method string, params ...any) json.RawMessage {
	o.t.Helper()
	var raw json.RawMessage
	o.call(&raw, method, params...)
	if schema, ok := o.evidence.schemas[method]; ok {
		if err := conformance.ValidateResult(schema, raw); err != nil {
			// A pinned official-schema finding is distinct from a direct state
			// assertion and from a public production rejection. Preserve all three.
			o.evidence.SchemaFindings = append(o.evidence.SchemaFindings, fmt.Sprintf("%s: %v", method, err))
		}
	}
	return raw
}

func (o *stateOracle) absent(method string, params ...any) {
	o.t.Helper()
	if got := o.raw(method, params...); !isNull(got) {
		o.t.Fatalf("direct %s should be absent: %s", method, got)
	}
}

func (o *stateOracle) quantity(method string, want uint64) {
	o.t.Helper()
	var got hexutil.Uint64
	o.call(&got, method)
	if uint64(got) != want {
		o.t.Fatalf("direct %s = %d, want %d", method, got, want)
	}
}

func (o *stateOracle) block(method string, ref any, full bool) wireBlock {
	o.t.Helper()
	raw := o.raw(method, ref, full)
	var block wireBlock
	if isNull(raw) {
		o.t.Fatalf("direct %s block missing", method)
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		o.t.Fatal(err)
	}
	var header types.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		o.t.Fatal(err)
	}
	if header.Hash() != block.Hash {
		o.t.Fatal("direct block header hash mismatch")
	}
	block.raw = raw
	return block
}

func (o *stateOracle) account(ref string, senderBalance, recipientBalance *big.Int, nonce uint64) {
	o.t.Helper()
	for _, account := range []struct {
		address string
		balance *big.Int
		nonce   uint64
	}{
		{anvil.Sender, senderBalance, nonce}, {anvil.Recipient, recipientBalance, 0},
	} {
		var balance hexutil.Big
		var gotNonce hexutil.Uint64
		o.call(&balance, "eth_getBalance", account.address, ref)
		o.call(&gotNonce, "eth_getTransactionCount", account.address, ref)
		if balance.ToInt().Cmp(account.balance) != 0 || uint64(gotNonce) != account.nonce {
			o.t.Fatalf("direct account %s at %s balance=%s nonce=%d; want %s/%d", account.address, ref, balance.ToInt(), gotNonce, account.balance, account.nonce)
		}
	}
}

func checkTransaction(t *testing.T, raw json.RawMessage, signed *types.Transaction, block *wireBlock) {
	t.Helper()
	var got *types.Transaction
	if err := json.Unmarshal(raw, &got); err != nil || got == nil {
		t.Fatalf("direct transaction missing/invalid: %s, %v", raw, err)
	}
	if transactionBytes(got) != transactionBytes(signed) {
		t.Fatal("direct transaction differs from signed bytes")
	}
	var inclusion struct {
		Hash        common.Hash     `json:"hash"`
		From        common.Address  `json:"from"`
		BlockHash   *common.Hash    `json:"blockHash"`
		BlockNumber *hexutil.Uint64 `json:"blockNumber"`
		Index       *hexutil.Uint64 `json:"transactionIndex"`
	}
	if err := json.Unmarshal(raw, &inclusion); err != nil {
		t.Fatal(err)
	}
	if inclusion.Hash != signed.Hash() || inclusion.From != common.HexToAddress(anvil.Sender) {
		t.Fatal("direct transaction identity differs")
	}
	if block == nil {
		if inclusion.BlockHash != nil || inclusion.BlockNumber != nil || inclusion.Index != nil {
			t.Fatal("direct pending transaction has inclusion context")
		}
	} else if inclusion.BlockHash == nil || *inclusion.BlockHash != block.Hash || inclusion.BlockNumber == nil || *inclusion.BlockNumber != block.Number || inclusion.Index == nil || *inclusion.Index != 0 {
		t.Fatal("direct transaction inclusion differs from mined block")
	}
}

func transactionBytes(tx *types.Transaction) string {
	if tx == nil {
		return "<nil>"
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return "marshal error: " + err.Error()
	}
	return hexutil.Encode(raw)
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func jsonEqual(a, b json.RawMessage) bool {
	var left, right any
	ld, rd := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	ld.UseNumber()
	rd.UseNumber()
	return ld.Decode(&left) == nil && rd.Decode(&right) == nil && reflect.DeepEqual(left, right)
}

type consumer struct {
	root      *ethguard.Client
	transport string
	view      bool
}

func openConsumers(t *testing.T, node *anvil.Node) []consumer {
	t.Helper()
	var consumers []consumer
	for _, transport := range []struct {
		name, endpoint string
		kind           config.Transport
	}{
		{"native-http", node.HTTP, config.TransportNativeHTTP}, {"raw-wss", node.WS, config.TransportWSS},
	} {
		root, err := ethguard.New(config.Config{ChainID: anvil.ChainID, CollectTimeout: "3s", Upstreams: []config.Upstream{
			{ID: "anvil-local", Endpoint: transport.endpoint, Transport: transport.kind},
		}})
		if err != nil {
			t.Fatalf("opening %s consumer: %v", transport.name, err)
		}
		t.Cleanup(root.Close)
		consumers = append(consumers, consumer{root, transport.name, false}, consumer{root, transport.name, true})
	}
	return consumers
}

// observe records an exact business-value assertion and the actual production
// result. A rejected legitimate oracle object is a failing, explicit baseline,
// not an allowed failure, skip, or successful typed read. Non-fatal assertions
// allow the independent lifecycle/recovery oracle to finish after a rejection.
func (c consumer) observe(t *testing.T, ctx context.Context, evidence *runEvidence, operation string, want any, wantErr error, read func(context.Context) (any, error)) {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	callCtx, err := ethguard.WithSourceEvidence(callCtx, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	callCtx, capture := ethguard.WithResult(callCtx)
	got, err := read(callCtx)
	result, published, captureErr := capture.Result()
	if !published || captureErr != nil {
		t.Fatalf("production result capture: published=%v err=%v", published, captureErr)
	}
	surface := "Root"
	if c.view {
		surface = "EthClient"
	}
	observation := productionObservation{Operation: operation, Surface: surface, Transport: c.transport, Want: want, Got: got, Result: result}
	if err != nil {
		observation.Error = err.Error()
	}
	if wantErr != nil {
		observation.WantError = wantErr.Error()
	}
	observation.Passed = reflect.DeepEqual(got, want) && ((wantErr == nil && err == nil) || (wantErr != nil && errors.Is(err, wantErr)))
	if result.Decision != nil && len(result.Decision.Support) > 1 {
		t.Error("local Anvil was counted as multiple sources")
	}
	if result.Uncertain == nil || result.Uncertain.Truncated || result.Uncertain.Dropped != 0 {
		t.Errorf("production raw evidence missing/truncated for %s", operation)
	}
	evidence.Production = append(evidence.Production, observation)
	if !observation.Passed {
		t.Errorf("production baseline %s/%s %s: got=%+v err=%v; want=%+v err=%v", c.transport, surface, operation, got, err, want, wantErr)
	}
}

type transactionValue struct {
	SignedBytes string
	Pending     bool
}

// transactionPayload bridges a public generated transaction union back to a
// geth transaction through its canonical JSON, so the observation compares the
// signed RLP bytes the oracle fixture carries.
func transactionPayload(tx *ethapi.Transaction) (*types.Transaction, error) {
	if tx == nil {
		return nil, nil
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		return nil, err
	}
	var out types.Transaction
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// receiptPayload bridges a public generated receipt back to a geth receipt
// through its canonical JSON, so the observation compares against the oracle's
// geth-decoded receipt with the same field semantics.
func receiptPayload(receipt *ethapi.Receipt) (*types.Receipt, error) {
	if receipt == nil {
		return nil, nil
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	var out types.Receipt
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c consumer) transaction(t *testing.T, ctx context.Context, evidence *runEvidence, stage string, signed *types.Transaction, pending bool) {
	t.Helper()
	c.observe(t, ctx, evidence, stage+"/TransactionByHash", transactionValue{transactionBytes(signed), pending}, nil, func(ctx context.Context) (any, error) {
		var tx *ethapi.Transaction
		var isPending bool
		var err error
		if c.view {
			tx, isPending, err = c.root.EthClient().TransactionByHash(ctx, ethapi.Hash(signed.Hash().Hex()))
		} else {
			tx, isPending, _, err = c.root.TransactionByHash(ctx, ethapi.Hash(signed.Hash().Hex()))
		}
		payload, bridgeErr := transactionPayload(tx)
		if bridgeErr != nil {
			return transactionValue{transactionBytes(payload), isPending}, errors.Join(err, bridgeErr)
		}
		return transactionValue{transactionBytes(payload), isPending}, err
	})
}

func (c consumer) transactionAbsent(t *testing.T, ctx context.Context, evidence *runEvidence, stage string, hash common.Hash) {
	t.Helper()
	c.observe(t, ctx, evidence, stage+"/TransactionByHash", transactionValue{"<nil>", false}, ethereum.NotFound, func(ctx context.Context) (any, error) {
		var tx *ethapi.Transaction
		var pending bool
		var err error
		if c.view {
			tx, pending, err = c.root.EthClient().TransactionByHash(ctx, ethapi.Hash(hash.Hex()))
		} else {
			tx, pending, _, err = c.root.TransactionByHash(ctx, ethapi.Hash(hash.Hex()))
		}
		payload, bridgeErr := transactionPayload(tx)
		if bridgeErr != nil {
			return transactionValue{transactionBytes(payload), pending}, errors.Join(err, bridgeErr)
		}
		return transactionValue{transactionBytes(payload), pending}, err
	})
}

func (c consumer) receipt(t *testing.T, ctx context.Context, evidence *runEvidence, stage string, hash common.Hash, want *types.Receipt) {
	t.Helper()
	var wantErr error
	if want == nil {
		wantErr = ethereum.NotFound
	}
	c.observe(t, ctx, evidence, stage+"/TransactionReceipt", want, wantErr, func(ctx context.Context) (any, error) {
		if c.view {
			value, err := c.root.EthClient().TransactionReceipt(ctx, ethapi.Hash(hash.Hex()))
			payload, bridgeErr := receiptPayload(value)
			if bridgeErr != nil {
				return payload, errors.Join(err, bridgeErr)
			}
			return payload, err
		}
		value, _, err := c.root.TransactionReceipt(ctx, ethapi.Hash(hash.Hex()))
		payload, bridgeErr := receiptPayload(value)
		if bridgeErr != nil {
			return payload, errors.Join(err, bridgeErr)
		}
		return payload, err
	})
}

type blockValue struct {
	Hash         common.Hash
	Number       uint64
	Transactions []string
}

func publicBlockValue(block *ethapi.Block) (*blockValue, error) {
	if block == nil {
		return nil, nil
	}
	number, err := rpc.ParseHexQuantity(string(block.Number))
	if err != nil {
		return nil, err
	}
	if block.Transactions.FullTransactions == nil {
		return nil, fmt.Errorf("block carries no full transactions")
	}
	value := &blockValue{Hash: common.HexToHash(string(block.Hash)), Number: number, Transactions: make([]string, 0, len(*block.Transactions.FullTransactions))}
	for _, union := range *block.Transactions.FullTransactions {
		raw, err := json.Marshal(union)
		if err != nil {
			return nil, err
		}
		var tx types.Transaction
		if err := json.Unmarshal(raw, &tx); err != nil {
			return nil, err
		}
		value.Transactions = append(value.Transactions, transactionBytes(&tx))
	}
	return value, nil
}

func (c consumer) included(t *testing.T, ctx context.Context, evidence *runEvidence, stage string, signed *types.Transaction, block wireBlock, receipt *types.Receipt) {
	t.Helper()
	c.transaction(t, ctx, evidence, stage, signed, false)
	c.receipt(t, ctx, evidence, stage, signed.Hash(), receipt)
	c.observe(t, ctx, evidence, stage+"/TransactionInBlock", transactionBytes(signed), nil, func(ctx context.Context) (any, error) {
		var tx *ethapi.Transaction
		var err error
		if c.view {
			tx, err = c.root.EthClient().TransactionInBlock(ctx, ethapi.Hash(block.Hash.Hex()), 0)
		} else {
			tx, _, err = c.root.TransactionInBlock(ctx, ethapi.Hash(block.Hash.Hex()), 0)
		}
		payload, bridgeErr := transactionPayload(tx)
		if bridgeErr != nil {
			return transactionBytes(payload), errors.Join(err, bridgeErr)
		}
		return transactionBytes(payload), err
	})
	wantBlock := &blockValue{block.Hash, uint64(block.Number), []string{transactionBytes(signed)}}
	for _, byHash := range []bool{true, false} {
		method := "BlockByNumber"
		if byHash {
			method = "BlockByHash"
		}
		c.observe(t, ctx, evidence, stage+"/"+method, wantBlock, nil, func(ctx context.Context) (any, error) {
			var got *ethapi.Block
			var err error
			if byHash {
				if c.view {
					got, err = c.root.EthClient().BlockByHash(ctx, ethapi.Hash(block.Hash.Hex()))
				} else {
					got, _, err = c.root.BlockByHash(ctx, ethapi.Hash(block.Hash.Hex()))
				}
			} else {
				number := new(big.Int).SetUint64(uint64(block.Number))
				if c.view {
					got, err = c.root.EthClient().BlockByNumber(ctx, number)
				} else {
					got, _, err = c.root.BlockByNumber(ctx, number)
				}
			}
			if err != nil {
				value, valueErr := publicBlockValue(got)
				if valueErr != nil {
					return nil, errors.Join(err, valueErr)
				}
				return value, err
			}
			return publicBlockValue(got)
		})
	}
}

func (c consumer) account(t *testing.T, ctx context.Context, evidence *runEvidence, stage string, block *big.Int, balance *big.Int, nonce uint64) {
	t.Helper()
	address := ethapi.Address(anvil.Sender)
	c.observe(t, ctx, evidence, stage+"/BalanceAt", balance, nil, func(ctx context.Context) (any, error) {
		if c.view {
			return c.root.EthClient().BalanceAt(ctx, address, block)
		}
		got, _, err := c.root.BalanceAt(ctx, address, block)
		return got, err
	})
	c.observe(t, ctx, evidence, stage+"/NonceAt", nonce, nil, func(ctx context.Context) (any, error) {
		if c.view {
			return c.root.EthClient().NonceAt(ctx, address, block)
		}
		got, _, err := c.root.NonceAt(ctx, address, block)
		return got, err
	})
}

type productionObservation struct {
	Operation string              `json:"operation"`
	Surface   string              `json:"surface"`
	Transport string              `json:"transport"`
	Want      any                 `json:"want"`
	Got       any                 `json:"got"`
	WantError string              `json:"want_error,omitempty"`
	Error     string              `json:"error,omitempty"`
	Passed    bool                `json:"passed"`
	Result    ethguard.CallResult `json:"result"`
}

type session struct {
	Provenance anvil.Provenance `json:"provenance"`
	Logs       string           `json:"logs"`
	Raw        []anvil.Record   `json:"raw"`
}

type recovery struct {
	Transaction         string `json:"transaction_visibility"`
	ReceiptRetained     bool   `json:"receipt_retained"`
	BlockByHashRetained bool   `json:"block_by_hash_retained"`
	PendingNonce        uint64 `json:"pending_nonce"`
	RestartIsolated     bool   `json:"restart_isolated"`
}

type runEvidence struct {
	Format         string                  `json:"format"`
	SpecVersion    string                  `json:"spec_version"`
	SpecSHA256     string                  `json:"spec_sha256"`
	GoVersion      string                  `json:"go_version,omitempty"`
	Revision       string                  `json:"revision,omitempty"`
	VCSModified    string                  `json:"vcs_modified,omitempty"`
	Description    string                  `json:"description"`
	Failed         bool                    `json:"failed"`
	Sessions       []session               `json:"sessions"`
	Production     []productionObservation `json:"production"`
	SchemaFindings []string                `json:"schema_findings,omitempty"`
	Recovery       recovery                `json:"snapshot_recovery"`
	dir            string
	schemas        map[string]any
}

func newEvidence(t *testing.T) *runEvidence {
	t.Helper()
	digest := sha256.Sum256(spec.MethodsJSON())
	evidence := &runEvidence{Format: "ethguard-anvil-transaction-v1", SpecVersion: spec.Version(), SpecSHA256: hex.EncodeToString(digest[:]),
		Description: "One pinned unforked local Anvil; separate native HTTP/raw WSS Root/EthClient observations are not independent provider evidence.",
		dir:         os.Getenv("ETHGUARD_ANVIL_ARTIFACT_DIR"),
	}
	if evidence.dir != "" {
		if err := os.Mkdir(evidence.dir, 0700); err != nil {
			t.Fatalf("artifact directory must be new: %v", err)
		}
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		evidence.GoVersion = build.GoVersion
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				evidence.Revision = setting.Value
			case "vcs.modified":
				evidence.VCSModified = setting.Value
			}
		}
	}
	// Some go-test toolchains omit VCS build settings. The integration owner
	// may supply the exact checkout revision without invoking git in this test.
	if evidence.Revision == "" {
		evidence.Revision = os.Getenv("ETHGUARD_EVIDENCE_REVISION")
	}
	var bundle struct {
		Results map[string]any `json:"results"`
	}
	decoder := json.NewDecoder(bytes.NewReader(spec.MethodsJSON()))
	decoder.UseNumber()
	if err := decoder.Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	evidence.schemas = bundle.Results
	return evidence
}

func (e *runEvidence) persist(t *testing.T) {
	t.Helper()
	if e.dir == "" {
		return
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Errorf("encoding Anvil evidence: %v", err)
		return
	}
	if len(data) > 16<<20 {
		t.Errorf("Anvil evidence exceeds 16 MiB bound (%d bytes); refusing incomplete artifact", len(data))
		return
	}
	path := filepath.Join(e.dir, "transaction-lifecycle.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Errorf("creating Anvil evidence: %v", err)
		return
	}
	_, writeErr := file.Write(append(data, '\n'))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Errorf("writing Anvil evidence: %v", err)
	}
}
