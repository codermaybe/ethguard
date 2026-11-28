package ethapi_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/ethobject/feehistory"
	receiptobjects "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

type resultParser func(json.RawMessage) (any, error)

func resultConstructor[T any](parse func(json.RawMessage) (T, error)) resultParser {
	return func(raw json.RawMessage) (any, error) { return parse(raw) }
}

func generatedResultParsers() map[string]resultParser {
	return map[string]resultParser{
		"eth_blobBaseFee":                       resultConstructor(ethapi.NewBlobBaseFeeResult),
		"eth_blockNumber":                       resultConstructor(ethapi.NewBlockNumberResult),
		"eth_call":                              resultConstructor(ethapi.NewCallResult),
		"eth_chainId":                           resultConstructor(ethapi.NewChainIdResult),
		"eth_createAccessList":                  resultConstructor(ethapi.NewCreateAccessListResult),
		"eth_estimateGas":                       resultConstructor(ethapi.NewEstimateGasResult),
		"eth_feeHistory":                        resultConstructor(ethapi.NewFeeHistoryResult),
		"eth_gasPrice":                          resultConstructor(ethapi.NewGasPriceResult),
		"eth_getBalance":                        resultConstructor(ethapi.NewGetBalanceResult),
		"eth_getBlockByHash":                    resultConstructor(ethapi.NewGetBlockByHashResult),
		"eth_getBlockByNumber":                  resultConstructor(ethapi.NewGetBlockByNumberResult),
		"eth_getBlockReceipts":                  resultConstructor(ethapi.NewGetBlockReceiptsResult),
		"eth_getCode":                           resultConstructor(ethapi.NewGetCodeResult),
		"eth_getHeaderByHash":                   resultConstructor(ethapi.NewGetHeaderByHashResult),
		"eth_getHeaderByNumber":                 resultConstructor(ethapi.NewGetHeaderByNumberResult),
		"eth_getLogs":                           resultConstructor(ethapi.NewGetLogsResult),
		"eth_getProof":                          resultConstructor(ethapi.NewGetProofResult),
		"eth_getStorageAt":                      resultConstructor(ethapi.NewGetStorageAtResult),
		"eth_getTransactionByHash":              resultConstructor(ethapi.NewGetTransactionByHashResult),
		"eth_getTransactionCount":               resultConstructor(ethapi.NewGetTransactionCountResult),
		"eth_getTransactionReceipt":             resultConstructor(ethapi.NewGetTransactionReceiptResult),
		"eth_maxPriorityFeePerGas":              resultConstructor(ethapi.NewMaxPriorityFeePerGasResult),
		"eth_sendRawTransaction":                resultConstructor(ethapi.NewSendRawTransactionResult),
		"eth_simulateV1":                        resultConstructor(ethapi.NewSimulateV1Result),
		"eth_syncing":                           resultConstructor(ethapi.NewSyncingResult),
		"net_listening":                         resultConstructor(ethapi.NewListeningResult),
		"net_peerCount":                         resultConstructor(ethapi.NewPeerCountResult),
		"eth_getBlockTransactionCountByHash":    resultConstructor(ethapi.NewGetBlockTransactionCountByHashResult),
		"eth_getBlockTransactionCountByNumber":  resultConstructor(ethapi.NewGetBlockTransactionCountByNumberResult),
		"eth_getTransactionByBlockHashAndIndex": resultConstructor(ethapi.NewGetTransactionByBlockHashAndIndexResult),
		"net_version":                           resultConstructor(ethapi.NewVersionResult),
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
	return methods.Results
}

// Source conformance is exercised in internal/spec/conformance. This test
// instead exercises each reachable generated result entry point, then checks
// its output against the effective schema and every declared input field.
// Source validation uses the original schema separately. Idempotence alone
// would also pass a parser that stably dropped transactions.
func TestGeneratedOfficialResultsPreserveContentAndSchema(t *testing.T) {
	parsers := generatedResultParsers()
	transactionTypes := map[string]bool{}
	blockShapes := map[string]bool{}
	schemas := officialResultSchemas(t)
	effective, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	vectors := officialVectors(t)
	counts := conformance.CountVectors(vectors)
	covered := map[string]int{}
	for _, vector := range vectors {
		if vector.Error != nil {
			continue
		}
		parse, ok := parsers[vector.Method]
		if !ok {
			t.Fatalf("%s: missing generated result constructor for %s", vector.Identity(), vector.Method)
		}
		covered[vector.Method]++
		if vector.Method == "eth_getTransactionByHash" && !ethcodec.IsJSONNull(vector.Result) {
			var tx struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(vector.Result, &tx); err != nil {
				t.Fatal(err)
			}
			transactionTypes[tx.Type] = true
		}
		if vector.Method == "eth_getBlockByHash" || vector.Method == "eth_getBlockByNumber" {
			if ethcodec.IsJSONNull(vector.Result) {
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
			t.Run("original-source", func(t *testing.T) {
				if err := conformance.ValidateResult(schemas[vector.Method], vector.Result); err != nil {
					t.Fatalf("original source vector/schema conflict: %v", err)
				}
			})
			t.Run("effective-generated-roundtrip", func(t *testing.T) {
				var schema any
				decoder := json.NewDecoder(bytes.NewReader(effective.Results[vector.Method]))
				decoder.UseNumber()
				if err := decoder.Decode(&schema); err != nil {
					t.Fatal(err)
				}
				assertOfficialRoundTrip(t, parse, schema, vector.Result)
			})
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
	// Every selected method now has pinned official success vectors; keep the
	// full selected set closed rather than retaining historical exceptions.
	for method := range schemas {
		count := counts[method]
		if count.Total != count.Results+count.Errors || covered[method] != count.Results {
			t.Errorf("%s corpus accounting: %+v, exercised results=%d", method, count, covered[method])
		}
		t.Logf("%s: %d interactions, %d results, %d top-level errors", method, count.Total, count.Results, count.Errors)
		if covered[method] == 0 {
			t.Errorf("official result method unexpectedly lacks vector coverage: %s", method)
		}
	}
}

func assertOfficialRoundTrip(t *testing.T, parse resultParser, schema any, raw json.RawMessage) json.RawMessage {
	t.Helper()
	expected, err := declaredResultProjection(schema, raw)
	if err != nil {
		t.Fatal(err)
	}
	return assertOfficialProjectionRoundTrip(t, parse, schema, raw, expected)
}

func assertOfficialProjectionRoundTrip(t *testing.T, parse resultParser, schema any, raw, expected json.RawMessage) json.RawMessage {
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
	out, err := ethcodec.MarshalNoEscape(value)
	if err != nil {
		t.Fatalf("generated marshal: %v", err)
	}
	if err := conformance.ValidateResult(schema, out); err != nil {
		t.Fatalf("generated output violates official schema: %v\n%s", err, out)
	}
	var before, after any
	inputDecoder := json.NewDecoder(bytes.NewReader(expected))
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
		t.Fatalf("generated output lost or changed official content:\n expected: %s\n output: %s", expected, out)
	}
	again, err := parse(out)
	if err != nil {
		t.Fatalf("generated canonical output cannot be reparsed: %v", err)
	}
	canonical, err := ethcodec.MarshalNoEscape(again)
	if err != nil || !bytes.Equal(out, canonical) {
		t.Fatalf("canonical output is not idempotent:\n first: %s\n second: %s (%v)", out, canonical, err)
	}
	return out
}

func firstObjectResult(t *testing.T, method string) json.RawMessage {
	t.Helper()
	for _, vector := range resultVectors(t, method) {
		if !ethcodec.IsJSONNull(vector.Result) {
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

func TestGeneratedGetLogsAdmitsReviewedUnionOverlap(t *testing.T) {
	source := officialResultSchemas(t)["eth_getLogs"]
	effective, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(effective.Results["eth_getLogs"], &schema); err != nil {
		t.Fatal(err)
	}
	if err := conformance.ValidateResult(source, json.RawMessage(`[]`)); err == nil {
		t.Fatal("upstream now accepts empty arrays; retire the erratum")
	}
	parse := resultConstructor(ethapi.NewGetLogsResult)
	for _, raw := range []string{
		`[]`,
		`["0x0000000000000000000000000000000000000000000000000000000000000001"]`,
		`[{"transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001"}]`,
	} {
		t.Run(raw, func(t *testing.T) { assertOfficialRoundTrip(t, parse, schema, json.RawMessage(raw)) })
	}
	for _, raw := range []string{`null`, `{}`, `1`, `true`, `["0x1"]`, `["0x0000000000000000000000000000000000000000000000000000000000000001",{}]`} {
		if _, err := ethapi.NewGetLogsResult(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid hashes/logs union admitted: %s", raw)
		}
	}
}

func TestGeneratedBlockTransactionsAnyOfAcceptsEmptyArray(t *testing.T) {
	value, err := ethapi.NewBlockTransactions(json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("anyOf empty transaction array must be admitted: %v", err)
	}
	out, err := ethcodec.MarshalNoEscape(value)
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
	assertOfficialRoundTrip(t, resultConstructor(ethapi.NewGetTransactionReceiptResult), officialResultSchemas(t)["eth_getTransactionReceipt"], raw)
	if _, err := receiptobjects.Encode(raw); err == nil {
		t.Fatal("protected receipt encoder must reject bloom/log disagreement")
	}
}

func TestFeeHistoryConsumersRejectStringGasUsedRatio(t *testing.T) {
	raw := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x1"],"gasUsedRatio":["0.5"]}`)
	if _, err := ethapi.NewFeeHistoryResult(raw); err == nil {
		t.Error("generated feeHistory result admitted a JSON string gasUsedRatio")
	}
	if _, err := feehistory.Encode(raw); err == nil {
		t.Error("protected feeHistory encoder admitted a JSON string gasUsedRatio")
	}
}

func TestGeneratedNonNullableResultsRejectNull(t *testing.T) {
	nullable := map[string]bool{
		"eth_getBlockTransactionCountByHash":    true,
		"eth_getBlockTransactionCountByNumber":  true,
		"eth_getTransactionByBlockHashAndIndex": true,
		"eth_getHeaderByHash":                   true,
		"eth_getHeaderByNumber":                 true,
		"eth_getBlockByHash":                    true,
		"eth_getBlockByNumber":                  true,
		"eth_getBlockReceipts":                  true,
		"eth_getTransactionByHash":              true,
		"eth_getTransactionReceipt":             true,
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
	out := assertOfficialRoundTrip(t, resultConstructor(ethapi.NewFeeHistoryResult), officialResultSchemas(t)["eth_feeHistory"], raw)
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
		if _, err := ethapi.NewFeeHistoryResult(invalid); err == nil {
			t.Errorf("official schema ratio bounds rounded away: %s", ratio)
		}
		if err := conformance.ValidateResult(officialResultSchemas(t)["eth_feeHistory"], invalid); err == nil {
			t.Errorf("independent oracle rounded away ratio bounds: %s", ratio)
		}
	}
}

func TestGeneratedBlockTransactionsMarshalRequiresExactlyOneMember(t *testing.T) {
	hashes := []ethapi.Hash{}
	transactions := []ethapi.BlockTransaction{}
	for _, tc := range []struct {
		name  string
		value ethapi.BlockTransactions
	}{
		{"unselected", ethapi.BlockTransactions{}},
		{"multiple selected", ethapi.BlockTransactions{TransactionHashes: &hashes, FullTransactions: &transactions}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := ethcodec.MarshalNoEscape(tc.value); err == nil {
				t.Fatalf("invalid tagged-union member count marshaled as %s", out)
			}
		})
	}
	// A selected empty branch is not an unselected union. The schema uses
	// anyOf, so either selected representation must retain an empty array.
	for _, value := range []ethapi.BlockTransactions{
		{TransactionHashes: &hashes},
		{FullTransactions: &transactions},
	} {
		out, err := ethcodec.MarshalNoEscape(value)
		if err != nil || string(out) != `[]` {
			t.Errorf("selected empty branch must marshal as []: %s (%v)", out, err)
		}
	}
}
