package ethguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const transactionCompatibilityBlock = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var transactionCompatibilityMethods = []string{
	methods.MethodGetTransactionByHash,
	methods.MethodGetTransactionByBlockHashAndIndex,
}

// This is a synthetic, locally signed fixture, not a provider observation.
// Assemble the official information object explicitly, independently of the
// production normalizer/encoder and Geth's payload-only JSON union encoder.
func transactionCompatibilityFixture(t *testing.T) (json.RawMessage, *types.Transaction) {
	t.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	signed, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(1), Nonce: 7, Gas: 30000, To: &to,
		Value: big.NewInt(1), GasFeeCap: big.NewInt(5), GasTipCap: big.NewInt(2),
		Data: []byte{0xab, 0xcd}, AccessList: types.AccessList{},
	}), types.NewLondonSigner(big.NewInt(1)), key)
	if err != nil {
		t.Fatal(err)
	}
	v, r, s := signed.RawSignatureValues()
	fields := map[string]any{
		"type": "0x2", "chainId": "0x1", "nonce": "0x7", "gas": "0x7530",
		"to": strings.ToLower(to.Hex()), "value": "0x1", "input": "0xabcd",
		"maxFeePerGas": "0x5", "maxPriorityFeePerGas": "0x2", "gasPrice": "0x3",
		"accessList": []any{}, "yParity": hexutil.EncodeBig(v), "v": hexutil.EncodeBig(v),
		"r": hexutil.EncodeBig(r), "s": hexutil.EncodeBig(s),
		"hash": signed.Hash().Hex(), "from": strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex()),
		"blockHash": transactionCompatibilityBlock, "blockNumber": "0x10",
		"blockTimestamp": "0x5", "transactionIndex": "0x0",
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := conformance.ValidateResult(receiptWireSchema(t, methods.MethodGetTransactionByHash), raw); err != nil {
		t.Fatalf("fixture violates official transaction shape: %v", err)
	}
	sender, err := types.Sender(types.NewLondonSigner(big.NewInt(1)), signed)
	if err != nil || sender != crypto.PubkeyToAddress(key.PublicKey) {
		t.Fatalf("invalid fixture signature: sender=%s err=%v", sender, err)
	}
	return raw, signed
}

func transactionCompatibilityEdit(t *testing.T, raw json.RawMessage, edits map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for key, value := range edits {
		if value == nil {
			delete(fields, key)
		} else {
			fields[key] = value
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func transactionCompatibilityRead(ctx context.Context, client *ethguard.Client, method string, hash ethapi.Hash) (*ethapi.Transaction, bool, ethguard.CallResult, error) {
	if method == methods.MethodGetTransactionByHash {
		return client.TransactionByHash(ctx, hash)
	}
	tx, result, err := client.TransactionInBlock(ctx, ethapi.Hash(transactionCompatibilityBlock), 0)
	return tx, false, result, err
}

func transactionCompatibilityView(ctx context.Context, client *ethguard.Client, method string, hash ethapi.Hash) (*ethapi.Transaction, bool, error) {
	if method == methods.MethodGetTransactionByHash {
		return client.EthClient().TransactionByHash(ctx, hash)
	}
	tx, err := client.EthClient().TransactionInBlock(ctx, ethapi.Hash(transactionCompatibilityBlock), 0)
	return tx, false, err
}

func transactionCompatibilityTyped(t *testing.T, got *ethapi.Transaction, want *types.Transaction) {
	t.Helper()
	if got == nil {
		t.Fatal("missing typed transaction")
	}
	// geth bridge: the generated object's official JSON decodes back into the
	// geth projection, whose signed payload must equal the fixture's.
	gotJSON, err := got.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var bridged types.Transaction
	if err := json.Unmarshal(gotJSON, &bridged); err != nil {
		t.Fatalf("generated transaction does not bridge to geth: %v", err)
	}
	gotBytes, err := bridged.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.MarshalBinary()
	if err != nil || !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("signed payload changed: got=%x want=%x err=%v", gotBytes, wantBytes, err)
	}
}

func transactionCompatibilityValue(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var a, b map[string]json.RawMessage
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("official Value changed: got=%s want=%s", got, want)
	}
}

// Unlike the existing basic transport tests, these assertions protect the
// complete official Value across case correction, vendor disagreement and
// reversed source order, through both public typed entry points.
func TestTransactionCompatibilityWholeKeysAndVendorTransport(t *testing.T) {
	raw, signed := transactionCompatibilityFixture(t)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	edits := map[string]json.RawMessage{"vendorTransaction": json.RawMessage(`"wss"`)}
	for _, key := range []string{"hash", "blockHash", "from", "to", "input"} {
		var text string
		if err := json.Unmarshal(fields[key], &text); err != nil {
			t.Fatal(err)
		}
		edits[key] = json.RawMessage(`"0x` + strings.ToUpper(text[2:]) + `"`)
	}
	upper := transactionCompatibilityEdit(t, raw, edits)
	lower := transactionCompatibilityEdit(t, raw, map[string]json.RawMessage{"vendorTransaction": json.RawMessage(`"http"`)})
	var canonicalValue json.RawMessage
	for _, method := range transactionCompatibilityMethods {
		for _, reverse := range []bool{false, true} {
			name := method + "/http-first"
			if reverse {
				name = method + "/wss-first"
			}
			t.Run(name, func(t *testing.T) {
				answer := func(payload json.RawMessage) func(sourceWireRequest) string {
					return func(req sourceWireRequest) string {
						if req.Method == method {
							return `"result":` + string(payload)
						}
						return `"result":"0x1"`
					}
				}
				http := newSourceWirePeer(t, false, answer(lower))
				wss := newSourceWirePeer(t, true, answer(upper))
				upstreams := []config.Upstream{
					{ID: "http", Endpoint: http.endpoint},
					{ID: "wss", Endpoint: wss.endpoint, Transport: config.TransportWSS},
				}
				if reverse {
					upstreams[0], upstreams[1] = upstreams[1], upstreams[0]
				}
				client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: upstreams})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.Close)
				base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(base, 5*time.Second)
				defer cancel()
				got, pending, result, err := transactionCompatibilityRead(ctx, client, method, ethapi.Hash(signed.Hash().Hex()))
				if err != nil || pending || !result.IsCorroborated() {
					t.Fatalf("Root: pending=%v result=%+v err=%v", pending, result, err)
				}
				d := result.Decision
				if len(d.Support) != 2 || len(d.Reject) != 0 || len(d.Faults) != 0 {
					t.Fatalf("vendor/case changed Core vote: %+v", d)
				}
				transactionCompatibilityValue(t, d.Value, raw)
				if canonicalValue == nil {
					canonicalValue = append(json.RawMessage(nil), d.Value...)
				} else if !bytes.Equal(canonicalValue, d.Value) {
					t.Fatalf("canonical bytes depend on method/source order: %s != %s", d.Value, canonicalValue)
				}
				if err := conformance.ValidateResult(receiptWireSchema(t, methods.MethodGetTransactionByHash), d.Value); err != nil {
					t.Fatalf("nonofficial output: %v", err)
				}
				transactionCompatibilityTyped(t, got, signed)
				if d.Diagnostics == nil || len(d.Diagnostics.Residuals)+len(d.Diagnostics.Conflicts) == 0 {
					t.Fatal("vendor disagreement lost its ancillary diagnostics")
				}
				if result.Uncertain == nil || len(result.Uncertain.Raw) != 2 {
					t.Fatalf("source evidence missing: %+v", result.Uncertain)
				}
				for _, source := range result.Uncertain.Raw {
					want := lower
					if source.Source.Name == "wss" {
						want = upper
					}
					transactionCompatibilityValue(t, source.Value, want)
				}
				view, viewPending, err := transactionCompatibilityView(ctx, client, method, ethapi.Hash(signed.Hash().Hex()))
				if err != nil || viewPending {
					t.Fatalf("EthClient: pending=%v err=%v", viewPending, err)
				}
				transactionCompatibilityTyped(t, view, signed)
				if http.httpConnections.Load() == 0 || wss.connections.Load() == 0 {
					t.Fatal("HTTP/raw WSS seam was not exercised")
				}
			})
		}
	}
}

func TestTransactionCompatibilityRejectsIdenticalInvalidSources(t *testing.T) {
	raw, signed := transactionCompatibilityFixture(t)
	cases := map[string]map[string]json.RawMessage{}
	for _, key := range []string{"type", "blockHash", "blockNumber", "blockTimestamp", "transactionIndex", "yParity", "accessList", "gasPrice"} {
		cases["missing-"+key] = map[string]json.RawMessage{key: nil}
	}
	cases["original-hash-conflict"] = map[string]json.RawMessage{"hash": json.RawMessage(`"` + transactionCompatibilityBlock + `"`)}
	cases["signature-payload-conflict"] = map[string]json.RawMessage{"r": json.RawMessage(`"0x1"`)}
	v, _, _ := signed.RawSignatureValues()
	cases["v-yParity-conflict"] = map[string]json.RawMessage{"v": json.RawMessage(`"` + hexutil.EncodeBig(new(big.Int).Xor(v, big.NewInt(1))) + `"`)}
	cases["mixed-pending-context"] = map[string]json.RawMessage{"blockHash": json.RawMessage(`null`)}
	for _, method := range transactionCompatibilityMethods {
		for name, edits := range cases {
			t.Run(method+"/"+name, func(t *testing.T) {
				client, nodes := fleet(t, 2)
				bad := transactionCompatibilityEdit(t, raw, edits)
				for _, node := range nodes {
					node.ReturnRaw(method, bad)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				got, _, result, err := transactionCompatibilityRead(ctx, client, method, ethapi.Hash(signed.Hash().Hex()))
				if got != nil || !errors.Is(err, ethguard.ErrMalformedResponse) || result.Decision == nil {
					t.Fatalf("identical invalid sources: tx=%v result=%+v err=%v input=%s", got, result, err, bad)
				}
				d := result.Decision
				if d.Status != rpc.Unknown || len(d.Value) != 0 || len(d.Support) != 0 || len(d.Faults) != 2 {
					t.Fatalf("invalid sources voted: %+v", d)
				}
				view, _, err := transactionCompatibilityView(ctx, client, method, ethapi.Hash(signed.Hash().Hex()))
				if view != nil || !errors.Is(err, ethguard.ErrMalformedResponse) {
					t.Fatalf("EthClient accepted invalid sources: tx=%v err=%v", view, err)
				}
			})
		}
	}
}

func TestTransactionCompatibilityPendingExplicitNulls(t *testing.T) {
	raw, signed := transactionCompatibilityFixture(t)
	edits := map[string]json.RawMessage{}
	for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		edits[key] = json.RawMessage(`null`)
	}
	pendingRaw := transactionCompatibilityEdit(t, raw, edits)
	t.Run("by-hash", func(t *testing.T) {
		client, nodes := fleet(t, 2)
		for _, node := range nodes {
			node.ReturnRaw(methods.MethodGetTransactionByHash, pendingRaw)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, pending, result, err := client.TransactionByHash(ctx, ethapi.Hash(signed.Hash().Hex()))
		if err != nil || !pending || !result.IsCorroborated() || len(result.Decision.Support) != 2 {
			t.Fatalf("pending: tx=%v pending=%v result=%+v err=%v", got, pending, result, err)
		}
		transactionCompatibilityValue(t, result.Decision.Value, pendingRaw)
		transactionCompatibilityTyped(t, got, signed)
		view, viewPending, err := client.EthClient().TransactionByHash(ctx, ethapi.Hash(signed.Hash().Hex()))
		if err != nil || !viewPending {
			t.Fatalf("EthClient pending=%v err=%v", viewPending, err)
		}
		transactionCompatibilityTyped(t, view, signed)
	})
	for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		t.Run("pending-missing-"+key, func(t *testing.T) {
			client, nodes := fleet(t, 2)
			bad := transactionCompatibilityEdit(t, pendingRaw, map[string]json.RawMessage{key: nil})
			for _, node := range nodes {
				node.ReturnRaw(methods.MethodGetTransactionByHash, bad)
			}
			got, _, result, err := client.TransactionByHash(context.Background(), ethapi.Hash(signed.Hash().Hex()))
			if key == "blockTimestamp" {
				if err != nil || got == nil || !result.IsCorroborated() || len(result.Decision.Support) != 2 {
					t.Fatalf("explicit pending state did not derive timestamp: tx=%v result=%+v err=%v", got, result, err)
				}
				transactionCompatibilityValue(t, result.Decision.Value, pendingRaw)
				transactionCompatibilityTyped(t, got, signed)
				return
			}
			if got != nil || !errors.Is(err, ethguard.ErrMalformedResponse) || result.Decision == nil || len(result.Decision.Faults) != 2 || len(result.Decision.Support) != 0 {
				t.Fatalf("missing pending %s silently became null: tx=%v result=%+v err=%v", key, got, result, err)
			}
		})
	}
	t.Run("by-block-rejects", func(t *testing.T) {
		client, nodes := fleet(t, 2)
		for _, node := range nodes {
			node.ReturnRaw(methods.MethodGetTransactionByBlockHashAndIndex, pendingRaw)
		}
		got, _, result, err := transactionCompatibilityRead(context.Background(), client, methods.MethodGetTransactionByBlockHashAndIndex, ethapi.Hash(signed.Hash().Hex()))
		if got != nil || !errors.Is(err, ethguard.ErrMalformedResponse) || result.Decision == nil || len(result.Decision.Faults) != 2 || len(result.Decision.Support) != 0 {
			t.Fatalf("pending accepted at block/index: tx=%v result=%+v err=%v", got, result, err)
		}
		view, err := client.EthClient().TransactionInBlock(context.Background(), ethapi.Hash(transactionCompatibilityBlock), 0)
		if view != nil || !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("EthClient accepted pending at block/index: tx=%v err=%v", view, err)
		}
	})
}

func TestTransactionCompatibilityFailuresKeepFrozenDenominator(t *testing.T) {
	raw, signed := transactionCompatibilityFixture(t)
	for _, method := range transactionCompatibilityMethods {
		for _, failure := range []string{"missing-required", "rpc-error"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				client, nodes := fleet(t, 4)
				nodes[0].ReturnRaw(method, raw)
				nodes[1].ReturnRaw(method, raw)
				nodes[2].ReturnRaw(method, transactionCompatibilityEdit(t, raw, map[string]json.RawMessage{"blockTimestamp": json.RawMessage(`"0x6"`)}))
				if failure == "missing-required" {
					nodes[3].ReturnRaw(method, transactionCompatibilityEdit(t, raw, map[string]json.RawMessage{"gasPrice": nil}))
				} else {
					nodes[3].Fail(method, -32000, "synthetic source failure")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				got, _, result, err := transactionCompatibilityRead(ctx, client, method, ethapi.Hash(signed.Hash().Hex()))
				if got != nil || !errors.Is(err, ethguard.ErrConflict) || result.Decision == nil {
					t.Fatalf("failure shrank denominator: tx=%v result=%+v err=%v", got, result, err)
				}
				d := result.Decision
				if d.Status != rpc.Unknown || len(d.Value) != 0 || len(d.Support) != 2 || len(d.Reject) != 1 || len(d.Faults) != 1 || len(d.Participants) != 4 {
					t.Fatalf("frozen membership/votes changed: %+v", d)
				}
			})
		}
	}
}
