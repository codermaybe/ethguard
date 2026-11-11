package ethguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// This oracle reads the pinned schema independently of the generated wire
// parser. Shape validation and typed fidelity must both hold after aggregation.
func receiptWireSchema(t *testing.T, method string) any {
	t.Helper()
	var bundle struct {
		Results map[string]any `json:"results"`
	}
	decoder := json.NewDecoder(bytes.NewReader(spec.MethodsJSON()))
	decoder.UseNumber()
	if err := decoder.Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Results[method] == nil {
		t.Fatalf("missing official schema for %s", method)
	}
	return bundle.Results[method]
}

func replayReceiptWire(t *testing.T, raw json.RawMessage, hash common.Hash) {
	t.Helper()
	schema := receiptWireSchema(t, methods.MethodGetTransactionReceipt)
	if err := conformance.ValidateResult(schema, raw); err != nil {
		t.Fatalf("input is not official wire: %v", err)
	}
	// Only the spelling changes; original captures are never rewritten.
	escaped := bytes.ReplaceAll(raw, []byte(`"0x`), []byte(`"\u0030x`))
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetTransactionReceipt {
				return `"result":` + string(raw)
			}
			return `"result":"0x1"`
		}),
		newSourceWirePeer(t, true, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetTransactionReceipt {
				return `"result":` + string(escaped)
			}
			return `"result":"0x1"`
		}),
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
	got, result, err := client.TransactionReceipt(ctx, hash)
	var want *types.Receipt
	if decodeErr := json.Unmarshal(raw, &want); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if want == nil {
		if !errors.Is(err, ethereum.NotFound) || got != nil {
			t.Fatalf("absence = %+v, %v", got, err)
		}
	} else if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("typed receipt differs from original upstream projection: got=%+v want=%+v err=%v", got, want, err)
	}
	if result.Decision == nil || !result.IsCorroborated() || len(result.Decision.Support) != 2 {
		t.Fatalf("lost agreement/evidence: %+v", result)
	}
	if err := conformance.ValidateResult(schema, result.Decision.Value); err != nil {
		t.Fatalf("protected output is not official wire: %v; %s", err, result.Decision.Value)
	}
	if want != nil {
		var input, output map[string]json.RawMessage
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(result.Decision.Value, &output); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"from", "to", "contractAddress"} {
			before, had := input[field]
			after, has := output[field]
			if had != has {
				t.Fatalf("field %s presence changed: %s -> %s", field, before, after)
			}
			if !had {
				continue
			}
			var a, b *string
			if err := json.Unmarshal(before, &a); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(after, &b); err != nil {
				t.Fatal(err)
			}
			if (a == nil) != (b == nil) || (a != nil && !strings.EqualFold(*a, *b)) {
				t.Fatalf("field %s meaning changed: %s -> %s", field, before, after)
			}
		}
	}
}

func TestReceiptWireOfficialVectors(t *testing.T) {
	paths, err := fs.Glob(spec.VectorsFS(), "eth_getTransactionReceipt/*.io")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		vectors, err := conformance.ParseVector(spec.VectorsFS(), path)
		if err != nil {
			t.Fatal(err)
		}
		for _, vector := range vectors {
			if vector.Error != nil {
				continue
			}
			t.Run(vector.Identity(), func(t *testing.T) {
				var request struct {
					Params []common.Hash `json:"params"`
				}
				if err := json.Unmarshal(vector.Request, &request); err != nil || len(request.Params) != 1 {
					t.Fatalf("invalid vector request: %s, %v", vector.Request, err)
				}
				replayReceiptWire(t, vector.Result, request.Params[0])
			})
		}
	}
}

// These are dated observations replayed locally, not new provider observations.
func TestReceiptWireCapturedReplay(t *testing.T) {
	_, records, err := replay.LoadBatch(filepath.Join("..", "..", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Method != methods.MethodGetTransactionReceipt {
			continue
		}
		t.Run(record.RequestID+"/"+record.Source.ID, func(t *testing.T) {
			var request struct {
				Params []common.Hash `json:"params"`
			}
			if err := json.Unmarshal([]byte(record.Request), &request); err != nil || len(request.Params) != 1 {
				t.Fatalf("invalid captured request: %v", err)
			}
			isError, body, err := replay.BodyOf(record)
			if err != nil || isError {
				t.Fatalf("captured response error: %v, %v", isError, err)
			}
			var response struct {
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			replayReceiptWire(t, response.Result, request.Params[0])
		})
	}
}

func TestReceiptWireRequiredFaultKeepsFrozenDenominator(t *testing.T) {
	for _, field := range []string{"from", "effectiveGasPrice", "type", "status"} {
		t.Run(field, func(t *testing.T) {
			client, nodes := fleet(t, 4)
			hash := common.HexToHash("0x42")
			good := fixtureReceipt(t, hash, 16)
			var bad, dissent map[string]json.RawMessage
			if err := json.Unmarshal(good, &bad); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(good, &dissent); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "type":
				bad[field] = json.RawMessage(`"0x100"`)
			case "status":
				bad[field] = json.RawMessage(`"0x2"`)
				// This passes the machine schema, but not the official outcome
				// description. Shape is not the method's semantic contract.
				raw, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				if err := conformance.ValidateResult(receiptWireSchema(t, methods.MethodGetTransactionReceipt), raw); err != nil {
					t.Fatalf("expected schema-valid semantic counterexample: %v", err)
				}
			default:
				delete(bad, field)
			}
			dissent["gasUsed"] = json.RawMessage(`"0x1"`)
			nodes[0].Return(methods.MethodGetTransactionReceipt, good)
			nodes[1].Return(methods.MethodGetTransactionReceipt, good)
			nodes[2].Return(methods.MethodGetTransactionReceipt, dissent)
			nodes[3].Return(methods.MethodGetTransactionReceipt, bad)
			got, result, err := client.TransactionReceipt(context.Background(), hash)
			if !errors.Is(err, rpc.ErrConflict) || got != nil || result.Decision == nil {
				t.Fatalf("invalid minority altered outcome: got=%+v result=%+v err=%v", got, result, err)
			}
			d := result.Decision
			if d.Status != rpc.Unknown || len(d.Value) != 0 || len(d.Support) != 2 || len(d.Reject) != 1 || len(d.Faults) != 1 {
				t.Fatalf("frozen evidence changed: %+v", d)
			}
		})
	}
}

func TestReceiptWireDoesNotMergeDistinctClaims(t *testing.T) {
	for _, method := range []string{methods.MethodGetTransactionReceipt, methods.MethodGetBlockReceipts} {
		for _, field := range []string{"to", "contractAddress", "data"} {
			t.Run(method+"/"+field, func(t *testing.T) {
				client, nodes := fleet(t, 2)
				hash := common.HexToHash("0x42")
				raw := fixtureReceipt(t, hash, 16)
				var a, b json.RawMessage
				if field == "data" {
					// Bloom does not commit log data, but the protected comparison does.
					a = receiptLogField(t, raw, "data", json.RawMessage(`"0x0000"`))
					b = receiptLogField(t, raw, "data", json.RawMessage(`"0xdeadbeef"`))
				} else {
					var object map[string]json.RawMessage
					if err := json.Unmarshal(raw, &object); err != nil {
						t.Fatal(err)
					}
					delete(object, field)
					var err error
					a, err = json.Marshal(object)
					if err != nil {
						t.Fatal(err)
					}
					object[field] = json.RawMessage(`null`)
					b, err = json.Marshal(object)
					if err != nil {
						t.Fatal(err)
					}
				}
				ref := hash.Hex()
				if method == methods.MethodGetBlockReceipts {
					a = append(append(json.RawMessage{'['}, a...), ']')
					b = append(append(json.RawMessage{'['}, b...), ']')
					ref = "0x10"
				}
				schema := receiptWireSchema(t, method)
				for _, input := range []json.RawMessage{a, b} {
					if err := conformance.ValidateResult(schema, input); err != nil {
						t.Fatalf("nonstandard test input: %v", err)
					}
				}
				nodes[0].Return(method, a)
				nodes[1].Return(method, b)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				result, err := client.Call(ctx, method, ref)
				if !errors.Is(err, rpc.ErrConflict) || result.Decision == nil {
					t.Fatalf("distinct valid claims merged: %+v, %v", result, err)
				}
				d := result.Decision
				if d.Status != rpc.Unknown || len(d.Value) != 0 || len(d.Support) != 1 || len(d.Reject) != 1 || len(d.Faults) != 0 {
					t.Fatalf("distinct claims misclassified: %+v", d)
				}
			})
		}
	}
}
