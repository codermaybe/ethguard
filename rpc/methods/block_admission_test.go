package methods

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
)

func TestBlockAdmissionRejectsMissingRequiredBody(t *testing.T) {
	raw, hash := admissionBlock(t, nil)
	p := BlockByHashPolicy().(blockPolicy)
	_, err := p.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByHash, Params: []any{hash.Hex(), true}}, raw, nil)
	if err == nil || !strings.Contains(err.Error(), "transactions") {
		t.Fatalf("expected missing transactions rejection, got %v", err)
	}
}

func TestBlockAdmissionRejectsTransactionRootMismatch(t *testing.T) {
	head := &types.Header{ParentHash: common.HexToHash("0x03"), UncleHash: types.EmptyUncleHash, TxHash: common.HexToHash("0x04"), Number: big.NewInt(1), GasLimit: 1, Time: 1, Difficulty: new(big.Int)}
	raw := blockRaw(t, head, nil)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"], _ = json.Marshal(head.Hash())
	// An empty body cannot produce the deliberately non-empty transaction root.
	raw, _ = json.Marshal(obj)
	p := BlockByHashPolicy().(blockPolicy)
	_, err := p.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByHash, Params: []any{head.Hash().Hex(), true}}, raw, nil)
	if err == nil || !strings.Contains(err.Error(), "transactions root") {
		t.Fatalf("expected transaction-root rejection, got %v", err)
	}
}

func TestBlockAdmissionFetchesAndBindsUnclesFromSameSource(t *testing.T) {
	uncle := &types.Header{ParentHash: common.HexToHash("0x01"), Number: big.NewInt(1), GasLimit: 1, Time: 1, Difficulty: new(big.Int)}
	uncleRaw, err := uncle.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	head := &types.Header{ParentHash: common.HexToHash("0x02"), UncleHash: types.CalcUncleHash([]*types.Header{uncle}), TxHash: types.EmptyTxsHash, Number: big.NewInt(1), GasLimit: 1, Time: 2, Difficulty: new(big.Int)}
	raw := blockRaw(t, head, []common.Hash{uncle.Hash()})
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	hash := head.Hash()
	obj["hash"], _ = json.Marshal(hash)
	raw, _ = json.Marshal(obj)
	p := BlockByHashPolicy().(blockPolicy)
	evidence, err := p.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByHash, Params: []any{hash.Hex(), true}}, raw, func(_ context.Context, method string, params []any) (json.RawMessage, error) {
		if method != "eth_getUncleByBlockHashAndIndex" || len(params) != 2 {
			t.Fatalf("unexpected uncle request %s %#v", method, params)
		}
		return uncleRaw, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) == 0 {
		t.Fatal("expected same-source uncle evidence")
	}
}

func admissionBlock(t *testing.T, txs []json.RawMessage) (json.RawMessage, common.Hash) {
	head := &types.Header{ParentHash: common.HexToHash("0x03"), UncleHash: types.EmptyUncleHash, TxHash: types.EmptyTxsHash, Number: big.NewInt(1), GasLimit: 1, Time: 1, Difficulty: new(big.Int)}
	raw := blockRaw(t, head, nil)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"], _ = json.Marshal(head.Hash())
	if txs != nil {
		obj["transactions"], _ = json.Marshal(txs)
	} else {
		delete(obj, "transactions")
	}
	out, _ := json.Marshal(obj)
	return out, head.Hash()
}

func blockRaw(t *testing.T, head *types.Header, uncles []common.Hash) json.RawMessage {
	t.Helper()
	raw, err := head.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["transactions"] = json.RawMessage(`[]`)
	if uncles == nil {
		obj["uncles"] = json.RawMessage(`[]`)
	} else {
		obj["uncles"], _ = json.Marshal(uncles)
	}
	out, _ := json.Marshal(obj)
	return out
}
