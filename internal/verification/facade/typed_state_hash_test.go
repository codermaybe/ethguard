package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// Hash-specific state reads are explicit pins: they skip anchor
// establishment and preserve go-ethereum's requireCanonical=false field.
func TestTypedStateHashWireAndViewMatch(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
		node.Return(methods.MethodGetTransactionCount, "0x2")
		node.Return(methods.MethodGetStorageAt, "0x"+strings.Repeat("11", 32))
		node.Return(methods.MethodGetCode, "0x6000")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addr := common.HexToAddress("0xdad")
	hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
	key := common.HexToHash("0x" + strings.Repeat("01", 32))

	balance, _, err := client.BalanceAtHash(ctx, addr, hash)
	if err != nil || balance.Int64() != 100 {
		t.Fatalf("root BalanceAtHash = %v, %v", balance, err)
	}
	nonce, _, err := client.NonceAtHash(ctx, addr, hash)
	if err != nil || nonce != 2 {
		t.Fatalf("root NonceAtHash = %d, %v", nonce, err)
	}
	storage, _, err := client.StorageAtHash(ctx, addr, key, hash)
	if err != nil || len(storage) != 32 || storage[0] != 0x11 {
		t.Fatalf("root StorageAtHash = %x, %v", storage, err)
	}
	code, _, err := client.CodeAtHash(ctx, addr, hash)
	if err != nil || string(code) != "\x60\x00" {
		t.Fatalf("root CodeAtHash = %x, %v", code, err)
	}

	view := client.EthClient()
	if got, err := view.BalanceAtHash(ctx, addr, hash); err != nil || got.Int64() != 100 {
		t.Fatalf("view BalanceAtHash = %v, %v", got, err)
	}
	if got, err := view.NonceAtHash(ctx, addr, hash); err != nil || got != 2 {
		t.Fatalf("view NonceAtHash = %d, %v", got, err)
	}
	if got, err := view.StorageAtHash(ctx, addr, key, hash); err != nil || len(got) != 32 {
		t.Fatalf("view StorageAtHash = %x, %v", got, err)
	}
	if got, err := view.CodeAtHash(ctx, addr, hash); err != nil || string(got) != "\x60\x00" {
		t.Fatalf("view CodeAtHash = %x, %v", got, err)
	}

	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBalance && call.Method != methods.MethodGetTransactionCount && call.Method != methods.MethodGetStorageAt && call.Method != methods.MethodGetCode {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal(call.Params[len(call.Params)-1], &obj); err != nil {
				t.Fatalf("%s ref is not object: %v", call.Method, err)
			}
			if obj["blockHash"] != hash.Hex() || obj["requireCanonical"] != false {
				t.Fatalf("%s hash ref = %#v", call.Method, obj)
			}
		}
	}
}

func TestTypedStateHashConflictAndMissing(t *testing.T) {
	client, nodes := fleet(t, 3)
	hash := common.HexToHash("0x" + strings.Repeat("cd", 32))
	for i, node := range nodes {
		node.Return(methods.MethodGetBalance, fmt.Sprintf("0x%x", i+1))
	}
	_, result, err := client.BalanceAtHash(context.Background(), common.Address{}, hash)
	if !errors.Is(err, ethguard.ErrConflict) || result.Decision == nil {
		t.Fatalf("hash conflict = result=%+v err=%v", result, err)
	}

	for _, node := range nodes {
		node.Fail(methods.MethodGetBalance, -32000, "missing block")
	}
	value, result, err := client.BalanceAtHash(context.Background(), common.Address{}, hash)
	if value != nil || !errors.Is(err, rpc.ErrNoValidResponse) || result.Decision == nil {
		t.Fatalf("hash missing = value=%v result=%+v err=%v", value, result, err)
	}
}
