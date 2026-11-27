package methods

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
)

type standardConsumerCase struct {
	valid         json.RawMessage
	invalid       json.RawMessage
	request       rpc.Request
	compatibility string
	scope         string
}

// This inventory contains executable evidence, never copied schema rules.
// Every registered read route must supply an actual CompilePlan execution and
// an independent effective-schema oracle for its standard-object branch. This
// does not extend that guarantee to other branches: Block reference/header
// contracts have separate executable evidence in standard_block_contract_test.
// Business bindings have their own
// positive/negative contracts next to each method; this table cannot declare
// a route consumed merely because an owner or constructor name exists.
func TestRegisteredStandardConsumption(t *testing.T) {
	bundle, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := executionapi.LoadManifest("../executionapi/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	identity := standardConsumerIdentity(t, bundle, manifest)
	vectors, err := conformance.ReadVectors(spec.VectorsFS())
	if err != nil {
		t.Fatal(err)
	}
	first := func(method string) json.RawMessage {
		for _, v := range vectors {
			if v.Method == method && v.Error == nil && !ethcodec.IsJSONNull(v.Result) {
				return append(json.RawMessage{}, v.Result...)
			}
		}
		t.Fatalf("missing official fixture for %s", method)
		return nil
	}
	fixtures := map[string]standardConsumerCase{}
	for _, method := range []string{MethodChainID, MethodGetBalance, MethodGetTransactionCount, MethodEstimateGas, MethodGasPrice, MethodMaxPriorityFeePerGas, MethodBlobBaseFee, MethodBlockNumber, MethodGetBlockTransactionCountByHash, MethodGetBlockTransactionCountByNumber} {
		fixtures[method] = standardConsumerCase{valid: json.RawMessage(`"0XA"`), invalid: json.RawMessage(`"0x01"`), compatibility: ethcodec.CompatWireHexCase, scope: "complete generated method root; method null/business policy retained"}
	}
	fixtures[MethodPeerCount] = standardConsumerCase{valid: json.RawMessage(`"0x000A"`), invalid: json.RawMessage(`"0XA"`), compatibility: ethcodec.CompatPeerQuantityZeros, scope: "complete generated method root"}
	fixtures[MethodNetVersion] = standardConsumerCase{valid: json.RawMessage(`"+0001"`), invalid: json.RawMessage(`"-1"`), compatibility: ethcodec.CompatLegacyDecimal, scope: "complete generated method root"}
	fixtures[MethodListening] = standardConsumerCase{valid: json.RawMessage(`true`), invalid: json.RawMessage(`null`), scope: "complete generated method root"}
	for _, method := range []string{MethodGetCode, MethodCall} {
		fixtures[method] = standardConsumerCase{valid: json.RawMessage(`"0x00AB"`), invalid: json.RawMessage(`"0x0"`), compatibility: ethcodec.CompatLowerPrefixHexCase, scope: "complete success result; call RPC errors remain a separate outcome contract"}
	}
	fixtures[MethodGetStorageAt] = standardConsumerCase{valid: json.RawMessage(`"0x` + strings.Repeat("AB", 32) + `"`), invalid: json.RawMessage(`"0x00"`), compatibility: ethcodec.CompatLowerPrefixHexCase, scope: "complete generated DATA root plus 32-byte method bound"}
	fixtures[MethodFeeHistory] = standardConsumerCase{valid: json.RawMessage(`{"oldestBlock":"0X1","baseFeePerGas":["0X1","0x2"],"gasUsedRatio":[0.5],"reward":null}`), invalid: json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":null}`), compatibility: "fee-history-members-v1; " + ethcodec.CompatWireHexCase, scope: "complete generated root after optional-null/member compatibility; range/ratio rules additional"}
	fixtures[MethodCreateAccessList] = standardConsumerCase{valid: json.RawMessage(`{"ACCESSLIST":[],"gasUsed":null,"error":null}`), invalid: json.RawMessage(`{"accessList":[],"gasUsed":"0x01"}`), compatibility: "access-list-members-v1; " + ethcodec.CompatWireHexCase, scope: "complete generated root after field construction; accessList additionally required"}
	fixtures[MethodSyncing] = standardConsumerCase{valid: json.RawMessage(`false`), invalid: json.RawMessage(`true`), compatibility: "syncing-object-projection-v1", scope: "complete generated method root"}
	fixtures[MethodSimulateV1] = standardConsumerCase{valid: json.RawMessage(`[]`), invalid: json.RawMessage(`null`), compatibility: "simulate-result-projection-v1", scope: "complete generated method root; business execution is not inferred"}
	log := json.RawMessage(`{"address":"0x` + strings.Repeat("a", 40) + `","topics":[],"data":"0x00AB","blockNumber":"0X1","transactionHash":"0x` + strings.Repeat("1", 64) + `","transactionIndex":"0x0","blockHash":"0x` + strings.Repeat("2", 64) + `","logIndex":"0x0","removed":false,"blockTimestamp":null}`)
	fixtures[MethodGetLogs] = standardConsumerCase{valid: append(append(json.RawMessage{'['}, log...), ']'), invalid: json.RawMessage(`[null]`), compatibility: "log-members-v1; " + ethcodec.CompatWireHexCase, scope: "complete Log constructors and result array; method-required inclusion fields additional"}
	for _, method := range []string{MethodGetBlockByHash, MethodGetBlockByNumber} {
		valid := first(method)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(valid, &fields); err != nil {
			t.Fatal(err)
		}
		fields["gasLimit"] = json.RawMessage(`"bad"`)
		invalid, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		fixtures[method] = standardConsumerCase{valid: valid, invalid: invalid, compatibility: "block-header-body-projection-v1", scope: "complete Block on full-object route; minimal reference reads and header-only reads retain separate explicit contracts"}
	}
	tx := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	var txFields struct{ Hash, BlockHash, TransactionIndex string }
	if err := json.Unmarshal(tx, &txFields); err != nil {
		t.Fatal(err)
	}
	fixtures[MethodGetTransactionByHash] = standardConsumerCase{valid: tx, invalid: json.RawMessage(`{}`), request: rpc.Request{Method: MethodGetTransactionByHash, Params: []any{txFields.Hash}}, compatibility: "transaction-mask-and-hex-v1; pending timestamp derivation is admission-owned", scope: "complete generated root then payload/hash/request binding"}
	fixtures[MethodGetTransactionByBlockHashAndIndex] = standardConsumerCase{valid: tx, invalid: json.RawMessage(`{}`), request: rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{txFields.BlockHash, txFields.TransactionIndex}}, compatibility: "transaction-mask-and-hex-v1", scope: "complete method root after shared transaction preparation; mined block/index binding"}
	receipt := sampleReceipt(t)
	fixtures[MethodGetTransactionReceipt] = standardConsumerCase{valid: receipt, invalid: json.RawMessage(`{}`), request: rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{sampleReceiptHash(t)}}, compatibility: "receipt-members-v1 and log-members-v1", scope: "complete Receipt plus null root; bloom/inclusion/request bindings additional"}
	receipts := append(append(json.RawMessage{'['}, receipt...), ']')
	fixtures[MethodGetBlockReceipts] = standardConsumerCase{valid: receipts, invalid: json.RawMessage(`[null]`), request: blockReceiptProcessingRequest(t, receipt), compatibility: "receipt-members-v1 and log-members-v1", scope: "complete per-receipt schemas and nullable array; ordering/block bindings additional"}
	if len(fixtures) != len(Specs()) {
		t.Fatalf("unregistered/untested contract: %d fixtures %d methods", len(fixtures), len(Specs()))
	}
	for _, registered := range Specs() {
		t.Run(registered.Method, func(t *testing.T) {
			fixture, ok := fixtures[registered.Method]
			if !ok {
				t.Fatal("registered route has no executable standard evidence")
			}
			schemaRaw := bundle.Results[registered.Method]
			if len(schemaRaw) == 0 {
				t.Fatal("registered route lacks official result")
			}
			selected := false
			for _, method := range manifest.ResultMethods {
				selected = selected || method == registered.Method
			}
			if !selected {
				t.Fatal("registered route omitted from Manifest")
			}
			var schema any
			decoder := json.NewDecoder(bytes.NewReader(schemaRaw))
			decoder.UseNumber()
			if err := decoder.Decode(&schema); err != nil {
				t.Fatal(err)
			}
			plan := compiledPlan(t, registered.Method)
			run := func(raw json.RawMessage) (json.RawMessage, error) {
				observations := []rpc.Observation{}
				for i := range 3 {
					observations = append(observations, rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: fmt.Sprint(i)}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}})
				}
				if plan.Canonical != nil {
					result, err := resolveCanonical(t, fixture.request, rpcengine.Corroboration{Floor: 2}, observations)
					return result.Decision.Value, err
				}
				result, err := plan.Resolver.Resolve(observations)
				return result.Decision.Value, err
			}
			original := append([]byte{}, fixture.valid...)
			value, err := run(fixture.valid)
			if err != nil || len(value) == 0 {
				t.Fatalf("valid route failed: %s: %v", value, err)
			}
			if !bytes.Equal(original, fixture.valid) {
				t.Fatal("compatibility rewrote raw provider evidence")
			}
			if err := conformance.ValidateResult(schema, value); err != nil {
				t.Fatalf("accepted output violates effective method root: %s: %v", value, err)
			}
			if value, err := run(fixture.invalid); err == nil || len(value) != 0 {
				t.Fatalf("invalid route produced a value: %s %v", value, err)
			}
			t.Logf("source=%s.result %s compatibility=%s scope=%s", registered.Method, identity, fixture.compatibility, fixture.scope)
		})
	}
}

// Consumption evidence is tied to the actual generated/source pair being
// exercised, rather than a self-reported constructor name or source path.
func standardConsumerIdentity(t *testing.T, bundle executionapi.Bundle, manifest executionapi.Manifest) string {
	t.Helper()
	auditRaw, err := os.ReadFile("../../ethapi/types.audit.json")
	if err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Version         string                        `json:"version"`
		ManifestSHA256  string                        `json:"manifestSHA256"`
		GeneratedSHA256 string                        `json:"generatedSHA256"`
		Errata          []executionapi.AppliedErratum `json:"errata"`
	}
	if err := json.Unmarshal(auditRaw, &audit); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile("../../ethapi/types.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest := fmt.Sprintf("%x", sha256.Sum256(generated))
	if audit.Version != bundle.Version || audit.ManifestSHA256 != manifest.SHA256 || audit.GeneratedSHA256 != generatedDigest {
		t.Fatal("consumer evidence has a stale source/Manifest/generated audit identity")
	}
	currentOverlay, err := json.Marshal(bundle.Errata)
	if err != nil {
		t.Fatal(err)
	}
	auditedOverlay, err := json.Marshal(audit.Errata)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(currentOverlay, auditedOverlay) {
		t.Fatal("consumer evidence has a stale Overlay application identity")
	}
	return fmt.Sprintf("version=%s manifest=%s generated=%s overlay=%x audit=%x", bundle.Version, manifest.SHA256, generatedDigest, sha256.Sum256(currentOverlay), sha256.Sum256(auditRaw))
}
