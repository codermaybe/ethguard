package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum/common"
)

func TestTypedHeaderByHashUsesHeaderOnlyRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw := fixtureBlock(t, 16)
	hash := common.HexToHash(blockHashOf(raw))
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	header, result, err := client.HeaderByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if header == nil || header.Number.Uint64() != 16 || !result.IsCorroborated() {
		t.Fatalf("header=%v result=%+v", header, result.Decision)
	}
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBlockByHash || len(call.Params) != 2 {
				continue
			}
			var full bool
			if err := json.Unmarshal(call.Params[1], &full); err != nil || full {
				t.Fatalf("node %d header request fullTx=%s", i, call.Params[1])
			}
		}
	}
}

func TestTypedHeaderByNumberMatchesViewAndRejectsPending(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw := fixtureBlock(t, 17)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	header, _, err := client.HeaderByNumber(ctx, big.NewInt(17))
	if err != nil || header == nil || header.Number.Uint64() != 17 {
		t.Fatalf("root header=%v err=%v", header, err)
	}
	viewHeader, err := client.EthClient().HeaderByNumber(ctx, big.NewInt(17))
	if err != nil || viewHeader == nil || viewHeader.Hash() != header.Hash() {
		t.Fatalf("view header=%v err=%v", viewHeader, err)
	}

	before := make([]int, len(nodes))
	for i, node := range nodes {
		before[i] = len(node.Calls())
	}
	if got, _, err := client.HeaderByNumber(ctx, big.NewInt(-1)); got != nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("pending header=%v err=%v", got, err)
	}
	for i, node := range nodes {
		if len(node.Calls()) != before[i] {
			t.Fatalf("pending header contacted node %d", i)
		}
	}
}
