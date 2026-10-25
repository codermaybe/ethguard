package ethguard_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// T09 uses only in-process synthetic responses. These fixtures build real
// signed transactions and real block headers so admission and typed decoding
// exercise their production checks; nothing here is written to testdata or
// presented as provider evidence.

const (
	t09AnchorHeight = uint64(0x10)
	t09Address      = "0x00000000000000000000000000000000000000aa"
	t09StorageKey   = "0x00000000000000000000000000000000000000000000000000000000000000aa"
)

var t09AnchorHash = "0x" + strings.Repeat("ab", 32)

type t09Chain struct {
	key     *ecdsa.PrivateKey
	tx      *types.Transaction
	tx2     *types.Transaction
	txHash  string
	txHash2 string
	from    string
	address common.Address
}

func t09NewChain(t *testing.T) *t09Chain {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress(t09Address)
	// A legacy transaction keeps gasPrice a concrete protected field; the
	// EIP-1559 wire shape renders it as JSON null, which the canonical
	// projection drops as absence rather than as a protected value.
	sign := func(tx *types.LegacyTx) *types.Transaction {
		signed, err := types.SignTx(types.NewTx(tx), types.LatestSignerForChainID(big.NewInt(1)), key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	tx := sign(&types.LegacyTx{
		Nonce:    1,
		GasPrice: big.NewInt(1),
		Gas:      21000,
		To:       &to,
		Value:    big.NewInt(0),
	})
	tx2 := sign(&types.LegacyTx{
		Nonce:    2,
		GasPrice: big.NewInt(2),
		Gas:      22000,
		To:       &to,
		Value:    big.NewInt(1),
	})
	return &t09Chain{
		key:     key,
		tx:      tx,
		tx2:     tx2,
		txHash:  tx.Hash().Hex(),
		txHash2: tx2.Hash().Hex(),
		from:    crypto.PubkeyToAddress(key.PublicKey).Hex(),
		address: to,
	}
}

// txObject renders the signed transaction in the RPC result shape. A mined
// transaction carries complete inclusion context; a pending one omits it.
func (c *t09Chain) txObject(mined bool, blockHash string, blockNumber, index uint64) json.RawMessage {
	return t09TxObject(c.tx, c.from, mined, blockHash, blockNumber, index)
}

func (c *t09Chain) tx2Object(mined bool, blockHash string, blockNumber, index uint64) json.RawMessage {
	return t09TxObject(c.tx2, c.from, mined, blockHash, blockNumber, index)
}

func t09TxObject(tx *types.Transaction, from string, mined bool, blockHash string, blockNumber, index uint64) json.RawMessage {
	body, err := json.Marshal(tx)
	if err != nil {
		panic(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		panic(err)
	}
	object["from"] = t09JSON(from)
	object["hash"] = t09JSON(tx.Hash().Hex())
	object["vendor"] = t09JSON("t09")
	if mined {
		object["blockHash"] = t09JSON(blockHash)
		object["blockNumber"] = t09JSON(fmt.Sprintf("0x%x", blockNumber))
		object["transactionIndex"] = t09JSON(fmt.Sprintf("0x%x", index))
	}
	return t09JSON(object)
}

// block builds a full post-merge block with exactly one signed transaction.
// The header commits to the transaction root and the empty uncle root, so
// admission can verify the body instead of trusting it.
func (c *t09Chain) block(number, timestamp uint64, gasUsed uint64) (json.RawMessage, string) {
	header := &types.Header{
		Number:      new(big.Int).SetUint64(number),
		GasLimit:    30_000_000,
		GasUsed:     gasUsed,
		Time:        timestamp,
		Difficulty:  big.NewInt(0),
		Extra:       []byte{},
		MixDigest:   common.Hash{},
		BaseFee:     big.NewInt(1_000_000_000),
		UncleHash:   types.EmptyUncleHash,
		ReceiptHash: types.EmptyReceiptsHash,
		TxHash:      types.DeriveSha(types.Transactions{c.tx, c.tx2}, trie.NewStackTrie(nil)),
	}
	hash := header.Hash().Hex()
	headerRaw, err := json.Marshal(header)
	if err != nil {
		t09Panic(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(headerRaw, &object); err != nil {
		t09Panic(err)
	}
	object["transactions"] = t09JSON([]json.RawMessage{
		c.txObject(true, hash, number, 0),
		c.tx2Object(true, hash, number, 1),
	})
	object["uncles"] = json.RawMessage(`[]`)
	// Response metadata outside the protected domain. Values differ between
	// representation variants while the header stays identical.
	object["size"] = json.RawMessage(`"0x100"`)
	object["totalDifficulty"] = json.RawMessage(`"0x1"`)
	object["vendor"] = t09JSON("t09")
	return t09JSON(object), hash
}

func (c *t09Chain) receipt(blockHash string, blockNumber uint64, gasUsed string) json.RawMessage {
	return c.receiptFor(c.tx, c.txHash, blockHash, blockNumber, 0, gasUsed)
}

func (c *t09Chain) receipt2(blockHash string, blockNumber uint64, gasUsed string) json.RawMessage {
	return c.receiptFor(c.tx2, c.txHash2, blockHash, blockNumber, 1, gasUsed)
}

func (c *t09Chain) receiptFor(tx *types.Transaction, txHash, blockHash string, blockNumber, txIndex uint64, gasUsed string) json.RawMessage {
	logs := []*types.Log{
		t09TypedLog(blockHash, txHash, blockNumber, txIndex, txIndex*2, "0x00ff"),
		t09TypedLog(blockHash, txHash, blockNumber, txIndex, txIndex*2+1, "0x0102"),
	}
	bloom := types.CreateBloom(&types.Receipt{Logs: logs})
	object := map[string]any{
		"transactionHash":   txHash,
		"transactionIndex":  fmt.Sprintf("0x%x", txIndex),
		"blockHash":         blockHash,
		"blockNumber":       fmt.Sprintf("0x%x", blockNumber),
		"from":              c.from,
		"cumulativeGasUsed": "0x5208",
		"gasUsed":           gasUsed,
		"effectiveGasPrice": "0x1",
		"logsBloom":         hexutil.Encode(bloom.Bytes()),
		"logs":              t09JSON(logs),
		"status":            "0x1",
		"type":              "0x2",
		"l1Fee":             "0x1",
		"vendor":            "t09",
	}
	return t09JSON(object)
}

func t09TypedLog(blockHash, txHash string, blockNumber, txIndex, logIndex uint64, data string) *types.Log {
	address := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	return &types.Log{
		Address:        address,
		Topics:         []common.Hash{common.HexToHash("0x" + strings.Repeat("11", 32))},
		Data:           common.FromHex(data),
		BlockNumber:    blockNumber,
		TxHash:         common.HexToHash(txHash),
		TxIndex:        uint(txIndex),
		BlockHash:      common.HexToHash(blockHash),
		Index:          uint(logIndex),
		Removed:        false,
		BlockTimestamp: 0x6553f100,
	}
}

func t09Log(blockHash, txHash string, blockNumber, index uint64, data string) json.RawMessage {
	return t09JSON(map[string]any{
		"address":          "0x00000000000000000000000000000000000000bb",
		"topics":           []string{"0x" + strings.Repeat("11", 32)},
		"data":             data,
		"blockNumber":      fmt.Sprintf("0x%x", blockNumber),
		"transactionHash":  txHash,
		"transactionIndex": "0x0",
		"blockHash":        blockHash,
		"logIndex":         fmt.Sprintf("0x%x", index),
		"removed":          false,
		"blockTimestamp":   "0x6553f100",
		"vendor":           "t09",
	})
}

func t09Logs(blockHash, txHash string) json.RawMessage {
	return t09JSON([]json.RawMessage{
		t09Log(blockHash, txHash, t09AnchorHeight, 0, "0x00ff"),
		t09Log(blockHash, txHash, t09AnchorHeight, 1, "0x0102"),
	})
}

func t09FeeHistory() json.RawMessage {
	return json.RawMessage(`{"oldestBlock":"0xf","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.5,0.25],"reward":[["0x1"],["0x2"]],"vendor":"t09"}`)
}

func t09AccessList() json.RawMessage {
	return json.RawMessage(`{"accessList":[{"address":"0x00000000000000000000000000000000000000cc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"]}],"gasUsed":"0x5208","vendor":"t09"}`)
}

// t09Fleet builds a real client over in-process JSON-RPC nodes. Chain-ID
// verification is disabled so every response fixture stays visible to the
// policy instead of being rejected at the pool identity gate.
type t09Fleet struct {
	client  *ethguard.Client
	adapter *ethadapter.Client
	nodes   []*rpctest.Node
	names   []string
}

func t09NewFleet(t *testing.T, count int) *t09Fleet {
	t.Helper()
	cfg := config.Config{}
	nodes := make([]*rpctest.Node, 0, count)
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		name := fmt.Sprintf("s%d", i)
		nodes = append(nodes, node)
		names = append(names, name)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{ID: name, Endpoint: node.URL()})
	}
	client, err := ethguard.New(cfg, ethguard.WithCollectTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return &t09Fleet{
		client:  client,
		adapter: ethadapter.Wrap(client),
		nodes:   nodes,
		names:   names,
	}
}

func (f *t09Fleet) context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (f *t09Fleet) returnRaw(method string, payloads []json.RawMessage) {
	for i, node := range f.nodes {
		node.ReturnRaw(method, payloads[i])
	}
}

func (f *t09Fleet) returnOne(method string, payload json.RawMessage) {
	for _, node := range f.nodes {
		node.ReturnRaw(method, payload)
	}
}

func (f *t09Fleet) failHTTP(method string, index int) {
	f.nodes[index].FailHTTP(method, 500)
}

func (f *t09Fleet) failProvider(method string, index int) {
	f.nodes[index].Fail(method, -32603, "t09 synthetic provider failure")
}

func (f *t09Fleet) calls(method string) int {
	total := 0
	for _, node := range f.nodes {
		for _, call := range node.Calls() {
			if call.Method == method {
				total++
			}
		}
	}
	return total
}

func (f *t09Fleet) totalCalls() int {
	total := 0
	for _, node := range f.nodes {
		total += len(node.Calls())
	}
	return total
}

// t09Variants builds three spellings of one semantic response: the base
// bytes, a reordered/indented form, and an uppercase/JSON-escaped form. The
// caller injects any vendor fields before calling, so the same field appears
// in every variant and only its position/spelling moves.
func t09Variants(t *testing.T, base json.RawMessage) []json.RawMessage {
	t.Helper()
	literal := t09VariantsNoEscape(t, base)
	return []json.RawMessage{literal[0], literal[1], t09EscapeFirstHex(t, literal[2])}
}

// t09VariantsNoEscape keeps the same key-order, whitespace, and hex-case
// variation without JSON string escapes. Block and transaction admission hand
// their wire values to geth's typed decoders, which require literal 0x-prefixed
// strings; escaped hex is an admission fault for those methods and is covered
// separately by TestVerificationEscapedHexAdmissionBoundary.
func t09VariantsNoEscape(t *testing.T, base json.RawMessage) []json.RawMessage {
	t.Helper()
	reordered := t09SemanticJSON(t, base)
	indented := t09Indent(t, reordered)
	upper := t09UpperHex(t, base)
	return []json.RawMessage{base, indented, upper}
}

// t09SemanticJSON re-encodes a response after JSON parsing. JSON escapes are
// semantically transparent, but geth's typed decoders read the literal wire
// bytes; decoding through this helper compares the typed meaning of a spelling
// against the canonical Value instead of rejecting the spelling itself.
func t09SemanticJSON(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return t09JSON(value)
}

func t09Indent(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func t09UpperHex(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	var walk func(any) any
	walk = func(item any) any {
		switch typed := item.(type) {
		case map[string]any:
			for key, child := range typed {
				typed[key] = walk(child)
			}
			return typed
		case []any:
			for i := range typed {
				typed[i] = walk(typed[i])
			}
			return typed
		case string:
			if len(typed) < 2 {
				return typed
			}
			if !strings.EqualFold(typed[:2], "0x") {
				return typed
			}
			return "0x" + strings.ToUpper(typed[2:])
		default:
			return item
		}
	}
	out, err := json.Marshal(walk(value))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// t09EscapeFirstHex replaces the first hexadecimal string's leading '0' with
// its JSON escape. The decoded value is unchanged, so only wire spelling moved.
func t09EscapeFirstHex(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	index := bytes.Index(raw, []byte(`"0x`))
	if index < 0 {
		index = bytes.Index(raw, []byte(`"0X`))
	}
	if index < 0 {
		return raw
	}
	out := make([]byte, 0, len(raw)+5)
	out = append(out, raw[:index+1]...)
	out = append(out, `\u0030`...)
	out = append(out, raw[index+2:]...)
	if !json.Valid(out) {
		t.Fatalf("escaped variant is not JSON: %s", out)
	}
	return out
}

func t09SetField(t *testing.T, raw json.RawMessage, key string, value any) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object[key] = t09JSON(value)
	return t09JSON(object)
}

func t09DeleteField(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, key)
	out, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func t09SetNestedField(t *testing.T, raw json.RawMessage, index int, key string, value any) json.RawMessage {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if index < 0 || index >= len(rows) {
		t.Fatalf("nested index %d out of range", index)
	}
	rows[index] = t09SetField(t, rows[index], key, value)
	out, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func t09DeleteNestedField(t *testing.T, raw json.RawMessage, index int, key string) json.RawMessage {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if index < 0 || index >= len(rows) {
		t.Fatalf("nested index %d out of range", index)
	}
	rows[index] = t09DeleteField(t, rows[index], key)
	out, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func t09JSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func t09Panic(err error) { panic(err) }

// Decoders below are lossless over the protected semantic value: the full
// typed header/body, every receipt, every log, and the transaction context
// fields the canonical encoder retains. A replacement of any protected field
// changes these summaries.

func t09DecodeBig(raw json.RawMessage) (string, error) {
	var value hexutil.Big
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return (*big.Int)(&value).String(), nil
}

// t09DecodeQuantity mirrors the quantity policies' value-preserving
// normalization: leading zeroes and hex case are spelling, not value. The
// production typed adapter only ever sees the canonical Value, so applying
// hexutil.Uint64 directly to a raw leading-zero spelling would test the wrong
// boundary.
func t09DecodeQuantity(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", err
	}
	if len(text) < 3 || !strings.EqualFold(text[:2], "0x") {
		return "", fmt.Errorf("invalid quantity %q", text)
	}
	value, ok := new(big.Int).SetString(text[2:], 16)
	if !ok || !value.IsUint64() {
		return "", fmt.Errorf("invalid quantity %q", text)
	}
	return value.String(), nil
}

func t09DecodeBytes(raw json.RawMessage) (string, error) {
	var value hexutil.Bytes
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return hexutil.Encode(value), nil
}

func t09DecodeDecimal(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", err
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return "", fmt.Errorf("invalid decimal %q", text)
	}
	return value.String(), nil
}

func t09DecodeBool(raw json.RawMessage) (string, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("true")) && !bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		return "", fmt.Errorf("not a boolean")
	}
	return fmt.Sprint(value), nil
}

type t09BlockRecord struct {
	Hash         common.Hash          `json:"hash"`
	Header       *types.Header        `json:"header"`
	Transactions []*types.Transaction `json:"transactions"`
	Uncles       []*types.Header      `json:"uncles"`
	Withdrawals  []*types.Withdrawal  `json:"withdrawals"`
}

func t09BlockSummary(block *types.Block) (string, error) {
	record := t09BlockRecord{
		Hash:         block.Hash(),
		Header:       block.Header(),
		Transactions: block.Transactions(),
		Uncles:       block.Uncles(),
		Withdrawals:  block.Withdrawals(),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func t09DecodeBlock(raw json.RawMessage) (string, error) {
	block, err := ethguard.DecodeBlock(raw)
	if err == ethereum.NotFound {
		return "notfound", nil
	}
	if err != nil {
		return "", err
	}
	return t09BlockSummary(block)
}

type t09ReceiptContext struct {
	From *common.Address `json:"from,omitempty"`
	To   *common.Address `json:"to,omitempty"`
}

func t09ReceiptContextOf(raw json.RawMessage) (t09ReceiptContext, error) {
	var context t09ReceiptContext
	if err := json.Unmarshal(raw, &context); err != nil {
		return t09ReceiptContext{}, err
	}
	return context, nil
}

func t09ReceiptFull(receipt *types.Receipt, context t09ReceiptContext) (string, error) {
	encoded, err := json.Marshal(struct {
		Receipt *types.Receipt  `json:"receipt"`
		From    *common.Address `json:"from,omitempty"`
		To      *common.Address `json:"to,omitempty"`
	}{Receipt: receipt, From: context.From, To: context.To})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func t09DecodeReceipt(raw json.RawMessage) (string, error) {
	receipt, err := ethguard.DecodeReceipt(raw)
	if err == ethereum.NotFound {
		return "notfound", nil
	}
	if err != nil {
		return "", err
	}
	context, err := t09ReceiptContextOf(raw)
	if err != nil {
		return "", err
	}
	return t09ReceiptFull(receipt, context)
}

type t09ReceiptsRecord struct {
	Receipts []string `json:"receipts"`
}

func t09ReceiptsFull(raw json.RawMessage, receipts []*types.Receipt) (string, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return "", err
	}
	if len(rows) != len(receipts) {
		return "", fmt.Errorf("receipts: %d rows, %d decoded", len(rows), len(receipts))
	}
	out := make([]string, len(receipts))
	for i, receipt := range receipts {
		context, err := t09ReceiptContextOf(rows[i])
		if err != nil {
			return "", err
		}
		out[i], err = t09ReceiptFull(receipt, context)
		if err != nil {
			return "", err
		}
	}
	encoded, err := json.Marshal(t09ReceiptsRecord{Receipts: out})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func t09DecodeBlockReceipts(raw json.RawMessage) (string, error) {
	receipts, err := ethguard.DecodeReceipts(raw)
	if err == ethereum.NotFound {
		return "notfound", nil
	}
	if err != nil {
		return "", err
	}
	return t09ReceiptsFull(raw, receipts)
}

type t09TransactionRecord struct {
	Transaction      *types.Transaction `json:"transaction"`
	Hash             *common.Hash       `json:"hash"`
	From             *common.Address    `json:"from"`
	BlockHash        *common.Hash       `json:"blockHash,omitempty"`
	BlockNumber      json.RawMessage    `json:"blockNumber,omitempty"`
	TransactionIndex json.RawMessage    `json:"transactionIndex,omitempty"`
	Pending          bool               `json:"pending"`
}

func t09DecodeTransaction(raw json.RawMessage) (string, error) {
	tx, pending, err := ethguard.DecodeTransaction(raw)
	if err == ethereum.NotFound {
		return "notfound", nil
	}
	if err != nil {
		return "", err
	}
	var wire struct {
		Hash             *common.Hash    `json:"hash"`
		From             *common.Address `json:"from"`
		BlockHash        *common.Hash    `json:"blockHash"`
		BlockNumber      json.RawMessage `json:"blockNumber"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(t09TransactionRecord{
		Transaction:      tx,
		Hash:             wire.Hash,
		From:             wire.From,
		BlockHash:        wire.BlockHash,
		BlockNumber:      wire.BlockNumber,
		TransactionIndex: wire.TransactionIndex,
		Pending:          pending,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// t09DecodeLogs keeps geth's full typed log, including blockTimestamp, so a
// replaced address, topic, data word, inclusion field, or removed flag changes
// the summary. Hex spelling is representation; the typed value compares by
// value, not by the wire's case.
func t09DecodeLogs(raw json.RawMessage) (string, error) {
	var logs []types.Log
	if err := json.Unmarshal(raw, &logs); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(logs)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type t09FeeWire struct {
	Oldest       *hexutil.Big     `json:"oldestBlock"`
	Base         []*hexutil.Big   `json:"baseFeePerGas"`
	Gas          []float64        `json:"gasUsedRatio"`
	Reward       [][]*hexutil.Big `json:"reward"`
	BaseBlob     []*hexutil.Big   `json:"baseFeePerBlobGas"`
	BlobGasRatio []float64        `json:"blobGasUsedRatio"`
}

func t09DecodeFee(raw json.RawMessage) (string, error) {
	var value t09FeeWire
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type t09AccessWire struct {
	AccessList types.AccessList `json:"accessList"`
	Error      string           `json:"error"`
	GasUsed    hexutil.Uint64   `json:"gasUsed"`
}

func t09DecodeAccess(raw json.RawMessage) (string, error) {
	var value t09AccessWire
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// t09DecodeSyncing treats the local whole answer as one value: canonical JSON
// keeps every field, so no protected local field can be dropped silently.
func t09DecodeSyncing(raw json.RawMessage) (string, error) {
	return rpc.CanonicalJSON(raw)
}
