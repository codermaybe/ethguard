package wire_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc/internal/objects/feehistory"
	receiptobjects "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

type resultParser func(json.RawMessage) (any, error)

func resultConstructor[T any](parse func(json.RawMessage) (T, error)) resultParser {
	return func(raw json.RawMessage) (any, error) { return parse(raw) }
}

func generatedResultParsers() map[string]resultParser {
	return map[string]resultParser{
		"eth_blobBaseFee":           resultConstructor(wire.NewBlobBaseFeeResult),
		"eth_blockNumber":           resultConstructor(wire.NewBlockNumberResult),
		"eth_call":                  resultConstructor(wire.NewCallResult),
		"eth_chainId":               resultConstructor(wire.NewChainIdResult),
		"eth_createAccessList":      resultConstructor(wire.NewCreateAccessListResult),
		"eth_estimateGas":           resultConstructor(wire.NewEstimateGasResult),
		"eth_feeHistory":            resultConstructor(wire.NewFeeHistoryResult),
		"eth_gasPrice":              resultConstructor(wire.NewGasPriceResult),
		"eth_getBalance":            resultConstructor(wire.NewGetBalanceResult),
		"eth_getBlockByHash":        resultConstructor(wire.NewGetBlockByHashResult),
		"eth_getBlockByNumber":      resultConstructor(wire.NewGetBlockByNumberResult),
		"eth_getBlockReceipts":      resultConstructor(wire.NewGetBlockReceiptsResult),
		"eth_getCode":               resultConstructor(wire.NewGetCodeResult),
		"eth_getLogs":               resultConstructor(wire.NewGetLogsResult),
		"eth_getProof":              resultConstructor(wire.NewGetProofResult),
		"eth_getStorageAt":          resultConstructor(wire.NewGetStorageAtResult),
		"eth_getTransactionByHash":  resultConstructor(wire.NewGetTransactionByHashResult),
		"eth_getTransactionCount":   resultConstructor(wire.NewGetTransactionCountResult),
		"eth_getTransactionReceipt": resultConstructor(wire.NewGetTransactionReceiptResult),
		"eth_maxPriorityFeePerGas":  resultConstructor(wire.NewMaxPriorityFeePerGasResult),
		"eth_sendRawTransaction":    resultConstructor(wire.NewSendRawTransactionResult),
		"eth_simulateV1":            resultConstructor(wire.NewSimulateV1Result),
		"eth_syncing":               resultConstructor(wire.NewSyncingResult),
		"net_version":               resultConstructor(wire.NewVersionResult),
	}
}

func officialResultSchemas(t *testing.T) map[string]any {
	t.Helper()
	var methods struct {
		Results map[string]any `json:"results"`
	}
	methodDecoder := json.NewDecoder(bytes.NewReader(spec.MethodsJSON()))
	methodDecoder.UseNumber()
	if err := methodDecoder.Decode(&methods); err != nil {
		t.Fatal(err)
	}
	var simulate struct {
		Method string `json:"method"`
		Result any    `json:"result"`
	}
	simulateDecoder := json.NewDecoder(bytes.NewReader(spec.SimulateV1JSON()))
	simulateDecoder.UseNumber()
	if err := simulateDecoder.Decode(&simulate); err != nil {
		t.Fatal(err)
	}
	methods.Results[simulate.Method] = simulate.Result
	return methods.Results
}

// Source conformance is exercised in internal/spec/conformance. This test
// instead exercises each reachable generated result entry point, then checks
// its output against the same official schema AND the complete input content.
// Idempotence alone would also pass a parser that stably dropped transactions.
func TestGeneratedOfficialResultsPreserveContentAndSchema(t *testing.T) {
	parsers := generatedResultParsers()
	transactionTypes := map[string]bool{}
	blockShapes := map[string]bool{}
	schemas := officialResultSchemas(t)
	covered := map[string]int{}
	for _, vector := range officialVectors(t) {
		if vector.Error != nil {
			continue
		}
		parse, ok := parsers[vector.Method]
		if !ok {
			t.Fatalf("%s: missing generated result constructor for %s", vector.Identity(), vector.Method)
		}
		covered[vector.Method]++
		if vector.Method == "eth_getTransactionByHash" && !wire.IsJSONNull(vector.Result) {
			var tx struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(vector.Result, &tx); err != nil {
				t.Fatal(err)
			}
			transactionTypes[tx.Type] = true
		}
		if vector.Method == "eth_getBlockByHash" || vector.Method == "eth_getBlockByNumber" {
			if wire.IsJSONNull(vector.Result) {
				blockShapes["null"] = true
			} else {
				var block struct {
					Transactions []json.RawMessage `json:"transactions"`
				}
				if err := json.Unmarshal(vector.Result, &block); err != nil {
					t.Fatal(err)
				}
				if len(block.Transactions) == 0 {
					blockShapes["empty"] = true
				} else if bytes.TrimSpace(block.Transactions[0])[0] == '"' {
					blockShapes["hashes"] = true
				} else {
					blockShapes["objects"] = true
				}
			}
		}
		t.Run(vector.Identity(), func(t *testing.T) {
			assertOfficialRoundTrip(t, parse, schemas[vector.Method], vector.Result)
		})
	}
	for _, transactionType := range []string{"0x0", "0x1", "0x2", "0x3", "0x4"} {
		if !transactionTypes[transactionType] {
			t.Errorf("missing official transaction branch %s", transactionType)
		}
	}
	for _, shape := range []string{"null", "empty", "hashes", "objects"} {
		if !blockShapes[shape] {
			t.Errorf("missing official block transactions branch %s", shape)
		}
	}
	// These pinned methods have no checked-in official success vectors. Do not
	// present synthetic inputs or constructor availability as vector coverage.
	withoutVectors := map[string]bool{
		"eth_gasPrice":             true,
		"eth_maxPriorityFeePerGas": true,
		"eth_simulateV1":           true,
	}
	for method := range schemas {
		if covered[method] == 0 {
			if !withoutVectors[method] {
				t.Errorf("official result method unexpectedly lacks vector coverage: %s", method)
			}
			t.Logf("no pinned official success vectors: %s", method)
		} else if withoutVectors[method] {
			t.Errorf("%s now has official vectors; remove its no-vector exception", method)
		}
	}
}

func assertOfficialRoundTrip(t *testing.T, parse resultParser, schema any, raw json.RawMessage) json.RawMessage {
	t.Helper()
	if schema == nil {
		t.Fatal("missing official result schema")
	}
	if err := conformance.ValidateResult(schema, raw); err != nil {
		t.Fatalf("input is not an official schema shape: %v", err)
	}
	value, err := parse(raw)
	if err != nil {
		t.Fatalf("generated constructor rejects official shape: %v", err)
	}
	out, err := wire.MarshalNoEscape(value)
	if err != nil {
		t.Fatalf("generated marshal: %v", err)
	}
	if err := conformance.ValidateResult(schema, out); err != nil {
		t.Fatalf("generated output violates official schema: %v\n%s", err, out)
	}
	var before, after any
	inputDecoder := json.NewDecoder(bytes.NewReader(raw))
	inputDecoder.UseNumber()
	if err := inputDecoder.Decode(&before); err != nil {
		t.Fatal(err)
	}
	outputDecoder := json.NewDecoder(bytes.NewReader(out))
	outputDecoder.UseNumber()
	if err := outputDecoder.Decode(&after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("generated output lost or changed official content:\n input: %s\n output: %s", raw, out)
	}
	again, err := parse(out)
	if err != nil {
		t.Fatalf("generated canonical output cannot be reparsed: %v", err)
	}
	canonical, err := wire.MarshalNoEscape(again)
	if err != nil || !bytes.Equal(out, canonical) {
		t.Fatalf("canonical output is not idempotent:\n first: %s\n second: %s (%v)", out, canonical, err)
	}
	return out
}

func firstObjectResult(t *testing.T, method string) json.RawMessage {
	t.Helper()
	for _, vector := range resultVectors(t, method) {
		if !wire.IsJSONNull(vector.Result) {
			return vector.Result
		}
	}
	t.Fatalf("no object result for %s", method)
	return nil
}

func TestGeneratedOptionalNullableFieldsPreserveAbsentNullAndValue(t *testing.T) {
	schemas := officialResultSchemas(t)
	parsers := generatedResultParsers()
	// Legacy transactions allow absent to, explicit null (creation), and an
	// address. Receipts independently allow all three for to/contractAddress.
	var legacy json.RawMessage
	for _, vector := range resultVectors(t, "eth_getTransactionByHash") {
		var tx map[string]json.RawMessage
		if err := json.Unmarshal(vector.Result, &tx); err != nil {
			t.Fatal(err)
		}
		if string(tx["type"]) == `"0x0"` {
			legacy = vector.Result
			break
		}
	}
	if legacy == nil {
		t.Fatal("no legacy transaction fixture")
	}
	for _, tc := range []struct {
		method string
		field  string
		raw    json.RawMessage
	}{
		{"eth_getTransactionByHash", "to", legacy},
		{"eth_getTransactionReceipt", "to", firstObjectResult(t, "eth_getTransactionReceipt")},
		{"eth_getTransactionReceipt", "contractAddress", firstObjectResult(t, "eth_getTransactionReceipt")},
	} {
		for _, state := range []string{"absent", "null", "value"} {
			t.Run(tc.method+"/"+tc.field+"/"+state, func(t *testing.T) {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(tc.raw, &object); err != nil {
					t.Fatal(err)
				}
				switch state {
				case "absent":
					delete(object, tc.field)
				case "null":
					object[tc.field] = json.RawMessage("null")
				case "value":
					object[tc.field] = json.RawMessage(`"0x0000000000000000000000000000000000000001"`)
				}
				raw, err := json.Marshal(object)
				if err != nil {
					t.Fatal(err)
				}
				assertOfficialRoundTrip(t, parsers[tc.method], schemas[tc.method], raw)
				if tc.method == "eth_getTransactionByHash" {
					var block map[string]json.RawMessage
					if err := json.Unmarshal(firstObjectResult(t, "eth_getBlockByNumber"), &block); err != nil {
						t.Fatal(err)
					}
					block["transactions"] = append(append(json.RawMessage{'['}, raw...), ']')
					nested, err := json.Marshal(block)
					if err != nil {
						t.Fatal(err)
					}
					assertOfficialRoundTrip(t, parsers["eth_getBlockByNumber"], schemas["eth_getBlockByNumber"], nested)
				}
			})
		}
	}
}

func TestGeneratedGetLogsAdmitsCompleteOfficialUnion(t *testing.T) {
	schema := officialResultSchemas(t)["eth_getLogs"]
	parse := resultConstructor(wire.NewGetLogsResult)
	for _, raw := range []string{
		`["0x0000000000000000000000000000000000000000000000000000000000000001"]`,
	} {
		t.Run(raw, func(t *testing.T) {
			assertOfficialRoundTrip(t, parse, schema, json.RawMessage(raw))
		})
	}
	// Empty arrays match BOTH oneOf branches in this pin and are therefore
	// rejected by the official type, even though the protected logs path keeps
	// its own project-level [] contract. Production is not switched here.
	for _, raw := range []string{`[]`, `null`, `{}`, `["0x1"]`, `["0x0000000000000000000000000000000000000000000000000000000000000001",{}]`} {
		if _, err := wire.NewGetLogsResult(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid hashes/logs union admitted: %s", raw)
		}
	}
}

func TestGeneratedBlockTransactionsAnyOfAcceptsEmptyArray(t *testing.T) {
	value, err := wire.NewBlockTransactions(json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("anyOf empty transaction array must be admitted: %v", err)
	}
	out, err := wire.MarshalNoEscape(value)
	if err != nil || string(out) != `[]` {
		t.Fatalf("empty block transaction array changed: %s (%v)", out, err)
	}
}

func TestGeneratedReceiptShapeDoesNotPromiseProtectedBloomSemantics(t *testing.T) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(firstObjectResult(t, "eth_getTransactionReceipt"), &object); err != nil {
		t.Fatal(err)
	}
	// This is a correctly-shaped bloom but cannot be the bloom of empty logs.
	object["logs"] = json.RawMessage(`[]`)
	object["logsBloom"] = json.RawMessage(`"0x` + string(bytes.Repeat([]byte{'f'}, 512)) + `"`)
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	assertOfficialRoundTrip(t, resultConstructor(wire.NewGetTransactionReceiptResult), officialResultSchemas(t)["eth_getTransactionReceipt"], raw)
	if _, err := receiptobjects.Encode(raw); err == nil {
		t.Fatal("protected receipt encoder must reject bloom/log disagreement")
	}
}

func TestFeeHistoryConsumersRejectStringGasUsedRatio(t *testing.T) {
	raw := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x1"],"gasUsedRatio":["0.5"]}`)
	if _, err := wire.NewFeeHistoryResult(raw); err == nil {
		t.Error("generated feeHistory result admitted a JSON string gasUsedRatio")
	}
	if _, err := feehistory.Encode(raw); err == nil {
		t.Error("protected feeHistory encoder admitted a JSON string gasUsedRatio")
	}
}

func TestGeneratedNonNullableResultsRejectNull(t *testing.T) {
	nullable := map[string]bool{
		"eth_getBlockByHash":        true,
		"eth_getBlockByNumber":      true,
		"eth_getBlockReceipts":      true,
		"eth_getTransactionByHash":  true,
		"eth_getTransactionReceipt": true,
	}
	for method, parse := range generatedResultParsers() {
		if nullable[method] {
			continue
		}
		t.Run(method, func(t *testing.T) {
			if _, err := parse(json.RawMessage(`null`)); err == nil {
				t.Fatal("nonnullable official result admitted JSON null")
			}
		})
	}
}

func TestOfficialFeeHistoryNumbersDoNotInheritProtectedFloatGate(t *testing.T) {
	// Official JSON numbers are exact decimal values. This value lies within
	// schema [0,1] but cannot survive the protected encoder's float64 key.
	raw := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x1"],"gasUsedRatio":[0.30000000000000001]}`)
	out := assertOfficialRoundTrip(t, resultConstructor(wire.NewFeeHistoryResult), officialResultSchemas(t)["eth_feeHistory"], raw)
	var result struct {
		GasUsedRatio []json.RawMessage `json:"gasUsedRatio"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.GasUsedRatio) != 1 || string(result.GasUsedRatio[0]) != "0.30000000000000001" {
		t.Fatalf("official number lost decimal precision: %s", out)
	}
	if _, err := feehistory.Encode(raw); err == nil {
		t.Fatal("protected float64 comparison key must not silently round a high-precision ratio")
	}
	for _, ratio := range []string{"1.00000000000000000001", "-0.00000000000000000001"} {
		invalid := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x1"],"gasUsedRatio":[` + ratio + `]}`)
		if _, err := wire.NewFeeHistoryResult(invalid); err == nil {
			t.Errorf("official schema ratio bounds rounded away: %s", ratio)
		}
		if err := conformance.ValidateResult(officialResultSchemas(t)["eth_feeHistory"], invalid); err == nil {
			t.Errorf("independent oracle rounded away ratio bounds: %s", ratio)
		}
	}
}

func TestGeneratedBlockTransactionsMarshalRequiresExactlyOneMember(t *testing.T) {
	hashes := []wire.Hash{}
	transactions := []wire.TransactionInformation{}
	for _, tc := range []struct {
		name  string
		value wire.BlockTransactions
	}{
		{"unselected", wire.BlockTransactions{}},
		{"multiple selected", wire.BlockTransactions{TransactionHashes: &hashes, FullTransactions: &transactions}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := wire.MarshalNoEscape(tc.value); err == nil {
				t.Fatalf("invalid tagged-union member count marshaled as %s", out)
			}
		})
	}
	// A selected empty branch is not an unselected union. The schema uses
	// anyOf, so either selected representation must retain an empty array.
	for _, value := range []wire.BlockTransactions{
		{TransactionHashes: &hashes},
		{FullTransactions: &transactions},
	} {
		out, err := wire.MarshalNoEscape(value)
		if err != nil || string(out) != `[]` {
			t.Errorf("selected empty branch must marshal as []: %s (%v)", out, err)
		}
	}
}
