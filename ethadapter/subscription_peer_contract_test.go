package ethadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/testkit/wstest"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
)

type bridgeBlock struct {
	header      *types.Header
	headerRaw   json.RawMessage
	blockRaw    json.RawMessage
	receiptsRaw json.RawMessage
	receipts    []*types.Receipt
	logs        []types.Log
}

// bridgeFixture builds independently signed payloads and both trie commitments.
// The small cases and the representative large case use the same proof path.
func bridgeFixture(t *testing.T, parent common.Hash, height uint64, marker byte, txCount, logCount int) *bridgeBlock {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	address := common.HexToAddress("0x1234")
	topic := crypto.Keccak256Hash([]byte("Changed(uint256)"))
	txs := make([]*types.Transaction, txCount)
	receipts := make([]*types.Receipt, txCount)
	logs := make([]types.Log, 0, logCount)
	for i := range txs {
		input := []byte{marker}
		if txCount >= 200 {
			input = bytes.Repeat([]byte{marker}, 128)
		}
		txs[i], err = types.SignTx(types.NewTx(&types.LegacyTx{Nonce: height*1000 + uint64(i), To: &address, Gas: 100000, GasPrice: big.NewInt(1), Value: new(big.Int), Data: input}), types.LatestSignerForChainID(big.NewInt(1)), key)
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = &types.Receipt{Status: 1, CumulativeGasUsed: uint64(i+1) * 21000, GasUsed: 21000, TxHash: txs[i].Hash(), BlockNumber: new(big.Int).SetUint64(height), TransactionIndex: uint(i), EffectiveGasPrice: big.NewInt(1), Logs: []*types.Log{}}
		for index := i; index < logCount; index += txCount {
			data := common.BigToHash(big.NewInt(int64(index + 1))).Bytes()
			logAddress, topics := address, []common.Hash{topic}
			if txCount >= 200 {
				data = bytes.Repeat(data, 4)
				topics = append(topics, common.BigToHash(big.NewInt(int64(i+1))), common.BigToHash(big.NewInt(int64(index+1))))
			}
			if marker == 0xf0 && i%2 == 1 {
				logAddress = common.HexToAddress("0x5678")
				topics = []common.Hash{crypto.Keccak256Hash([]byte("Other(uint256)"))}
			}
			receipts[i].Logs = append(receipts[i].Logs, &types.Log{Address: logAddress, Topics: topics, Data: data})
		}
		receipts[i].Bloom = types.CreateBloom(receipts[i])
	}
	head := &types.Header{ParentHash: parent, UncleHash: types.EmptyUncleHash, Root: common.HexToHash("0x11"), TxHash: types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil)), ReceiptHash: types.DeriveSha(types.Receipts(receipts), trie.NewStackTrie(nil)), Difficulty: new(big.Int), Number: new(big.Int).SetUint64(height), GasLimit: 30_000_000, GasUsed: uint64(txCount) * 21000, Time: 1000 + height, Extra: []byte{marker}}
	for _, receipt := range receipts {
		for i, v := range receipt.Bloom {
			head.Bloom[i] |= v
		}
	}
	txRows := make([]map[string]json.RawMessage, txCount)
	receiptRows := make([]map[string]json.RawMessage, txCount)
	for i, tx := range txs {
		receipts[i].BlockHash = head.Hash()
		for _, log := range receipts[i].Logs {
			log.BlockHash, log.BlockNumber, log.BlockTimestamp = head.Hash(), height, head.Time
			log.TxHash, log.TxIndex, log.Index = tx.Hash(), uint(i), uint(len(logs))
			logs = append(logs, *log)
		}
		txRows[i] = contractTransactionFields(t, tx)
		txRows[i]["blockHash"] = contractJSON(t, head.Hash())
		txRows[i]["blockNumber"] = contractJSON(t, hexutil.EncodeUint64(height))
		txRows[i]["blockTimestamp"] = contractJSON(t, hexutil.EncodeUint64(head.Time))
		txRows[i]["transactionIndex"] = contractJSON(t, hexutil.EncodeUint64(uint64(i)))
		receiptRows[i] = contractFields(t, receipts[i])
		receiptRows[i]["from"] = txRows[i]["from"]
		receiptRows[i]["to"] = contractJSON(t, address)
		delete(receiptRows[i], "root")
	}
	fields := contractHeaderFields(t, head)
	fields["transactions"], fields["uncles"], fields["size"] = contractJSON(t, txRows), json.RawMessage("[]"), contractJSON(t, "0x100")
	return &bridgeBlock{header: head, headerRaw: contractJSON(t, contractHeaderFields(t, head)), blockRaw: contractJSON(t, fields), receiptsRaw: contractJSON(t, receiptRows), receipts: receipts, logs: logs}
}

type bridgeRequest struct {
	ID     json.RawMessage
	Method string
	Params []json.RawMessage
}
type bridgeWireConn interface {
	ReadMessage() ([]byte, error)
	WriteText([]byte) error
	Close() error
}

type bridgePeer struct {
	url          string
	mu           sync.Mutex
	writeMu      sync.Mutex
	connections  map[bridgeWireConn]bool
	subs         map[string]bridgeWireConn
	blocks       map[common.Hash]*bridgeBlock
	requests     []bridgeRequest
	changed      chan struct{}
	stopped      chan struct{}
	stopOnce     sync.Once
	gate         <-chan struct{}
	seq          uint64
	server       *httptest.Server
	stopListener func()
	wg           sync.WaitGroup
}

func newBridgePeer(t *testing.T, blocks ...*bridgeBlock) *bridgePeer {
	t.Helper()
	p := &bridgePeer{connections: make(map[bridgeWireConn]bool), subs: make(map[string]bridgeWireConn), blocks: make(map[common.Hash]*bridgeBlock), changed: make(chan struct{}, 1), stopped: make(chan struct{})}
	for _, block := range blocks {
		p.blocks[block.header.Hash()] = block
	}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wstest.Upgrade(t, w, r)
		if err != nil {
			return
		}
		p.serve(t, r.Context(), conn)
	}))
	p.url = "ws" + strings.TrimPrefix(p.server.URL, "http")
	t.Cleanup(p.stop)
	return p
}

func (p *bridgePeer) serve(t *testing.T, ctx context.Context, conn bridgeWireConn) {
	p.mu.Lock()
	select {
	case <-p.stopped:
		p.mu.Unlock()
		_ = conn.Close()
		return
	default:
	}
	p.connections[conn] = true
	p.wg.Add(1)
	p.mu.Unlock()
	defer p.wg.Done()
	defer func() {
		_ = conn.Close()
		p.mu.Lock()
		delete(p.connections, conn)
		for id, c := range p.subs {
			if c == conn {
				delete(p.subs, id)
			}
		}
		p.mu.Unlock()
		p.signal()
	}()
	for {
		raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req bridgeRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("bridge peer request: %v", err)
			return
		}
		p.mu.Lock()
		p.requests = append(p.requests, req)
		gate := p.gate
		p.mu.Unlock()
		p.signal()
		if gate != nil && req.Method == "eth_subscribe" {
			select {
			case <-gate:
			case <-p.stopped:
				return
			case <-ctx.Done():
				return
			}
		}
		result := json.RawMessage("null")
		switch req.Method {
		case "eth_chainId":
			result = json.RawMessage(`"0x1"`)
		case "eth_subscribe":
			if len(req.Params) != 1 || string(req.Params[0]) != `"newHeads"` {
				t.Errorf("bridge must register native newHeads, got %s", raw)
				return
			}
			p.mu.Lock()
			p.seq++
			id := fmt.Sprintf("bridge-%d", p.seq)
			p.subs[id] = conn
			p.mu.Unlock()
			result = contractJSON(t, id)
		case "eth_unsubscribe":
			var id string
			if len(req.Params) == 1 {
				_ = json.Unmarshal(req.Params[0], &id)
			}
			p.mu.Lock()
			delete(p.subs, id)
			p.mu.Unlock()
			result = json.RawMessage("true")
		case "eth_getBlockByHash", "eth_getBlockReceipts":
			var hash common.Hash
			if len(req.Params) > 0 {
				if err := json.Unmarshal(req.Params[0], &hash); err != nil {
					var ref struct{ BlockHash common.Hash }
					if json.Unmarshal(req.Params[0], &ref) == nil {
						hash = ref.BlockHash
					}
				}
			}
			p.mu.Lock()
			block := p.blocks[hash]
			p.mu.Unlock()
			if block != nil {
				if req.Method == "eth_getBlockReceipts" {
					result = block.receiptsRaw
				} else if len(req.Params) > 1 && string(req.Params[1]) == "true" {
					result = block.blockRaw
				} else {
					result = block.headerRaw
				}
			}
		default:
			t.Errorf("unexpected subscription RPC %s", req.Method)
		}
		response := contractJSON(t, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		p.writeMu.Lock()
		err = conn.WriteText(response)
		p.writeMu.Unlock()
		if err != nil {
			return
		}
		p.signal()
	}
}

func (p *bridgePeer) signal() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}
func (p *bridgePeer) stop() {
	p.stopOnce.Do(func() {
		close(p.stopped)
		p.mu.Lock()
		for conn := range p.connections {
			_ = conn.Close()
		}
		p.mu.Unlock()
		if p.server != nil {
			p.server.Close()
		}
		if p.stopListener != nil {
			p.stopListener()
		}
		p.wg.Wait()
	})
}

func (p *bridgePeer) wait(t *testing.T, predicate func([]bridgeRequest, int) bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		ok := predicate(p.requests, len(p.subs))
		p.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-p.changed:
		case <-deadline.C:
			t.Fatal("subscription peer condition did not occur")
		}
	}
}

func (p *bridgePeer) push(t *testing.T, block *bridgeBlock) {
	t.Helper()
	p.wait(t, func(_ []bridgeRequest, n int) bool { return n > 0 })
	p.mu.Lock()
	targets := make(map[string]bridgeWireConn, len(p.subs))
	for id, conn := range p.subs {
		targets[id] = conn
	}
	p.mu.Unlock()
	for id, conn := range targets {
		raw := contractJSON(t, map[string]any{"jsonrpc": "2.0", "method": "eth_subscription", "params": map[string]any{"subscription": id, "result": block.headerRaw}})
		p.writeMu.Lock()
		err := conn.WriteText(raw)
		p.writeMu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("notification write: %v", err)
		}
	}
}

func bridgeAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-ch:
		if !ok {
			t.Fatal("caller channel unexpectedly closed")
		}
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("subscription delivery timed out")
		var zero T
		return zero
	}
}
