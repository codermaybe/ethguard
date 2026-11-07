package ethguard_test

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum/common"
)

// scalarWireLog keeps the request evidence for both transports. The typed
// assertions below therefore cover the JSON-RPC method and parameter shape,
// rather than only checking the projected value returned to the caller.
type scalarWireLog struct {
	mu       sync.Mutex
	requests []sourceWireRequest
}

func (l *scalarWireLog) add(req sourceWireRequest) {
	l.mu.Lock()
	l.requests = append(l.requests, req)
	l.mu.Unlock()
}

func (l *scalarWireLog) method(method string) []sourceWireRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []sourceWireRequest
	for _, req := range l.requests {
		if req.Method == method {
			out = append(out, req)
		}
	}
	return out
}

func scalarTransportAnswer(log *scalarWireLog) func(sourceWireRequest) string {
	return func(req sourceWireRequest) string {
		log.add(req)
		switch req.Method {
		case methods.MethodNetVersion:
			return `"result":"1"`
		case methods.MethodGetBalance:
			return `"result":"0x64"`
		case methods.MethodGetTransactionCount:
			return `"result":"0x7"`
		case methods.MethodGetStorageAt:
			return `"result":"0x` + strings.Repeat("ab", 32) + `"`
		case methods.MethodGetCode:
			return `"result":"0x6000"`
		case methods.MethodGasPrice:
			return `"result":"0x3b9aca00"`
		case methods.MethodBlobBaseFee:
			return `"result":"0x5"`
		default:
			return `"result":"0x1"`
		}
	}
}

// TestTypedScalarTransportMatrix covers the already-frozen scalar and node
// state methods through native HTTP and raw WSS. Hash-pinned state reads avoid
// the moving-tag anchor path, so this test isolates the transport boundary.
func TestTypedScalarTransportMatrix(t *testing.T) {
	logs := []*scalarWireLog{{}, {}}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, scalarTransportAnswer(logs[0])),
		newSourceWirePeer(t, true, scalarTransportAnswer(logs[1])),
	}
	client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
		{ID: "http", Endpoint: peers[0].endpoint},
		{ID: "wss", Endpoint: peers[1].endpoint, Transport: config.TransportWSS},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	address := common.HexToAddress("0xdad")
	key := common.HexToHash("0x01")
	pin := common.HexToHash("0x" + strings.Repeat("11", 32))

	networkID, networkResult, err := client.NetworkID(ctx)
	if err != nil || networkID.Cmp(big.NewInt(1)) != 0 || networkResult.Decision == nil || networkResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("NetworkID = %v, result=%+v, err=%v", networkID, networkResult, err)
	}

	balance, balanceResult, err := client.BalanceAtHash(ctx, address, pin)
	if err != nil || balance.Cmp(big.NewInt(100)) != 0 || balanceResult.Decision == nil || balanceResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("BalanceAtHash = %v, result=%+v, err=%v", balance, balanceResult, err)
	}
	nonce, nonceResult, err := client.NonceAtHash(ctx, address, pin)
	if err != nil || nonce != 7 || nonceResult.Decision == nil || nonceResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("NonceAtHash = %d, result=%+v, err=%v", nonce, nonceResult, err)
	}
	storage, storageResult, err := client.StorageAtHash(ctx, address, key, pin)
	if err != nil || string(storage) != string(common.Hex2Bytes(strings.Repeat("ab", 32))) || storageResult.Decision == nil || storageResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("StorageAtHash = %x, result=%+v, err=%v", storage, storageResult, err)
	}
	code, codeResult, err := client.CodeAtHash(ctx, address, pin)
	if err != nil || string(code) != string([]byte{0x60, 0x00}) || codeResult.Decision == nil || codeResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("CodeAtHash = %x, result=%+v, err=%v", code, codeResult, err)
	}

	gasPrice, gasResult, err := client.SuggestGasPrice(ctx)
	if err != nil || gasPrice.Cmp(big.NewInt(1_000_000_000)) != 0 || gasResult.Decision == nil || gasResult.Decision.Status != rpc.Selected {
		t.Fatalf("SuggestGasPrice = %v, result=%+v, err=%v", gasPrice, gasResult, err)
	}
	blobFee, blobResult, err := client.BlobBaseFee(ctx)
	if err != nil || blobFee.Cmp(big.NewInt(5)) != 0 || blobResult.Decision == nil || blobResult.Decision.Status != rpc.Selected {
		t.Fatalf("BlobBaseFee = %v, result=%+v, err=%v", blobFee, blobResult, err)
	}

	for i, log := range logs {
		for _, method := range []string{
			methods.MethodNetVersion,
			methods.MethodGetBalance,
			methods.MethodGetTransactionCount,
			methods.MethodGetStorageAt,
			methods.MethodGetCode,
			methods.MethodGasPrice,
			methods.MethodBlobBaseFee,
		} {
			if got := log.method(method); len(got) == 0 {
				t.Fatalf("source %d did not receive %s", i, method)
			}
		}
		for _, req := range log.method(methods.MethodGetBalance) {
			if len(req.Params) != 2 || string(req.Params[1]) != `{"blockHash":"`+pin.Hex()+`","requireCanonical":false}` {
				t.Fatalf("source %d BalanceAtHash params = %s, want EIP-1898 hash object", i, req.Params)
			}
		}
		for _, method := range []string{methods.MethodGetTransactionCount, methods.MethodGetCode} {
			for _, req := range log.method(method) {
				if len(req.Params) != 2 || string(req.Params[1]) != `{"blockHash":"`+pin.Hex()+`","requireCanonical":false}` {
					t.Fatalf("source %d %s params = %s, want EIP-1898 hash object", i, method, req.Params)
				}
			}
		}
		for _, req := range log.method(methods.MethodGetStorageAt) {
			if len(req.Params) != 3 || string(req.Params[2]) != `{"blockHash":"`+pin.Hex()+`","requireCanonical":false}` {
				t.Fatalf("source %d StorageAtHash params = %s, want EIP-1898 hash object", i, req.Params)
			}
		}
	}
}
