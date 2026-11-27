package methods

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestTransactionByHashDefinition(t *testing.T) {
	spec := transactionByHashSpec()
	descriptor := transactionByHashDescriptor()
	if spec.Method != MethodGetTransactionByHash || descriptor.Name != spec.Method || descriptor.Adapter != "TransactionByHash" {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Admit != nil || spec.Normalize == nil || spec.Encode != nil || spec.ProcessCanonical == nil || spec.Resolver != ResolverStrict {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionRequiredFieldsRejectBeforeVoting(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	for _, field := range []string{"blockTimestamp", "yParity", "accessList", "gasPrice"} {
		t.Run(field, func(t *testing.T) {
			missing := deleteField(t, raw, field)
			request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{txHash(t, raw)}}
			if _, err := processTransactionByHash(context.Background(), request, missing, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("missing %s admission = %v", field, err)
			}
			if _, err := normalizeTransactionByHash(missing); err == nil {
				t.Fatalf("missing %s contributed a comparison key", field)
			}
			decision, err := resolveCanonical(t, request, rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
				{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: missing}}},
				{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: missing}}},
			})
			if err == nil || decision.Decision.Status == rpc.Corroborated {
				t.Fatalf("identical incomplete objects corroborated: %+v, %v", decision, err)
			}
		})
	}
}

func TestTransactionMethodRulesShareCanonicalOutput(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	upper := foldHex(t, injectField(t, raw, "vendorExtension", `{"nonce":9007199254740993}`))
	want, err := normalizeTransactionByHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{txHash(t, raw)}}
	if _, err := processTransactionByHash(context.Background(), request, upper, nil); err != nil {
		t.Fatalf("whole-key case compatibility rejected: %v", err)
	}
	got, err := normalizeTransactionByHash(upper)
	if err != nil || got != want {
		t.Fatalf("case compatibility = %s, %v; want %s", got, err, want)
	}
	for _, observations := range [][]rpc.Observation{
		{{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}}, {Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: upper}}}},
		{{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: upper}}}, {Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}}},
	} {
		decision, err := resolveCanonical(t, request, rpcengine.Corroboration{Floor: 2}, observations)
		if err != nil || decision.Decision.Status != rpc.Corroborated || string(decision.Decision.Value) != want {
			t.Fatalf("canonical decision = %+v, %v; want %s", decision, err, want)
		}
	}
}

func observedPendingTransaction(t *testing.T) (rpc.Request, json.RawMessage, []byte) {
	t.Helper()
	data, err := os.ReadFile("testdata/transaction/anvil-1.7.1-pending.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Kind             string `json:"kind"`
		ProviderEvidence bool   `json:"provider_evidence"`
		State            string `json:"state"`
		Request          string `json:"request"`
		Response         string `json:"response"`
		RequestSHA256    string `json:"request_sha256"`
		ResponseSHA256   string `json:"response_sha256"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Kind != "local-anvil-observation" || fixture.ProviderEvidence || fixture.State != "pending" {
		t.Fatalf("unexpected observation provenance: %+v", fixture)
	}
	for _, item := range []struct{ raw, digest string }{
		{fixture.Request, fixture.RequestSHA256},
		{fixture.Response, fixture.ResponseSHA256},
	} {
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(item.raw))); got != item.digest {
			t.Fatalf("observation SHA256 = %s, want %s", got, item.digest)
		}
	}
	var request struct {
		Method string   `json:"method"`
		Params []string `json:"params"`
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(fixture.Request), &request); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fixture.Response), &response); err != nil {
		t.Fatal(err)
	}
	if request.Method != MethodGetTransactionByHash || len(request.Params) != 1 || request.Params[0] != txHash(t, response.Result) {
		t.Fatalf("observation request does not bind transaction: %+v", request)
	}
	return rpc.Request{Method: request.Method, Params: []any{request.Params[0]}}, response.Result, []byte(fixture.Response)
}

func TestTransactionByHashPendingTimestampPreservesEvidenceAndPayload(t *testing.T) {
	request, raw, response := observedPendingTransaction(t)
	before, responseBefore := bytes.Clone(raw), bytes.Clone(response)
	var original map[string]json.RawMessage
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if _, exists := original["blockTimestamp"]; exists {
		t.Fatal("raw Anvil observation must omit blockTimestamp")
	}
	for _, key := range []string{"blockHash", "blockNumber", "transactionIndex"} {
		if !bytes.Equal(original[key], []byte("null")) {
			t.Fatalf("raw %s = %s, want explicit null", key, original[key])
		}
	}
	candidate, err := prepareTransactionByHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(candidate, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["blockTimestamp"]) != "null" || len(fields) != len(original)+1 {
		t.Fatalf("candidate must add exactly blockTimestamp:null: %s", candidate)
	}
	for key, value := range original {
		if !bytes.Equal(fields[key], value) {
			t.Fatalf("official %s changed: %s -> %s", key, value, fields[key])
		}
	}
	var originalTx, candidateTx types.Transaction
	if err := json.Unmarshal(raw, &originalTx); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(candidate, &candidateTx); err != nil {
		t.Fatal(err)
	}
	originalPayload, err := originalTx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	candidatePayload, err := candidateTx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalPayload, candidatePayload) || originalTx.Hash() != candidateTx.Hash() || originalTx.Hash().Hex() != request.Params[0] {
		t.Fatal("pending derivation changed signed payload or hash")
	}
	if _, err := processTransactionByHash(context.Background(), request, raw, nil); err != nil {
		t.Fatal(err)
	}
	explicit := injectField(t, raw, "blockTimestamp", `null`)
	decision, err := resolveCanonical(t, request, rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{ID: "derived"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
		{Attempted: true, Source: rpc.SourceRef{ID: "explicit"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: explicit}}},
	})
	if err != nil || decision.Decision.Status != rpc.Corroborated || !bytes.Equal(decision.Decision.Value, candidate) {
		t.Fatalf("pending canonical Value = %+v, %v", decision, err)
	}
	if !bytes.Equal(raw, before) || !bytes.Equal(response, responseBefore) || bytes.Contains(raw, []byte("blockTimestamp")) {
		t.Fatal("candidate construction mutated raw evidence")
	}
}

func TestTransactionByHashPendingTimestampRequiresExplicitPendingState(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	for _, key := range []string{"blockHash", "blockNumber", "transactionIndex"} {
		for _, form := range []string{"missing", "non-null"} {
			t.Run(key+"/"+form, func(t *testing.T) {
				mutated := deleteField(t, raw, key)
				if form == "non-null" {
					value := `"0x1"`
					if key == "blockHash" {
						value = fmt.Sprintf("%q", request.Params[0])
					}
					mutated = injectField(t, raw, key, value)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(mutated, &fields); err != nil {
					t.Fatal(err)
				}
				if _, matched, err := correctTransactionByHashKey("blockTimestamp", nil, fields); err != nil || matched {
					t.Fatalf("incomplete/non-pending context derived timestamp: matched=%v, %v", matched, err)
				}
				if _, err := prepareTransactionByHash(mutated); !errors.Is(err, rpc.ErrMalformedResponse) {
					t.Fatalf("invalid context acquired canonical Value: %v", err)
				}
				if _, err := processTransactionByHash(context.Background(), request, mutated, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
					t.Fatalf("invalid context admitted: %v", err)
				}
			})
		}
	}
	for _, value := range []string{`"0x1"`, `false`, `0`} {
		if _, err := prepareTransactionByHash(injectField(t, raw, "blockTimestamp", value)); !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("explicit non-null timestamp %s was repaired: %v", value, err)
		}
	}
	mined := injectField(t, raw, "blockHash", fmt.Sprintf("%q", request.Params[0]))
	mined = injectField(t, mined, "blockNumber", `"0x1"`)
	mined = injectField(t, mined, "transactionIndex", `"0x0"`)
	if _, err := prepareTransactionByHash(mined); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("mined missing timestamp was repaired: %v", err)
	}
}

func TestTransactionByHashLegacyChainIDMatchesSignedPayload(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	conflicting := injectField(t, raw, "chainId", `"0x1"`)
	if _, err := processTransactionByHash(context.Background(), request, conflicting, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		var tx types.Transaction
		if decodeErr := json.Unmarshal(conflicting, &tx); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		decision, resolveErr := resolveCanonical(t, request, rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
			{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: conflicting}}},
			{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: conflicting}}},
		})
		t.Fatalf("declared chainId=1 conflicts with signed chainId=%s but admission returned %v; status=%v support=%d resolveErr=%v Value=%s", tx.ChainId(), err, decision.Decision.Status, len(decision.Decision.Support), resolveErr, decision.Decision.Value)
	}
}

func TestTransactionByHashPendingTimestampDoesNotRescueInvalidPayload(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	for _, edit := range []struct{ key, value string }{
		{"hash", `"0x0000000000000000000000000000000000000000000000000000000000000001"`},
		{"r", `"0x1"`},
		{"type", `"0x7f"`},
	} {
		t.Run(edit.key, func(t *testing.T) {
			invalid := injectField(t, raw, edit.key, edit.value)
			before := bytes.Clone(invalid)
			_, admissionErr := processTransactionByHash(context.Background(), request, invalid, nil)
			if !errors.Is(admissionErr, rpc.ErrMalformedResponse) {
				t.Fatalf("pending correction rescued invalid %s: %v", edit.key, admissionErr)
			}
			decision, err := resolveCanonical(t, request, rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
				{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
				{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
				{Attempted: true, Source: rpc.SourceRef{ID: "c"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage("null")}}},
				{Attempted: true, Source: rpc.SourceRef{ID: "d"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: admissionErr}}},
			})
			if !errors.Is(err, rpc.ErrConflict) || decision.Decision.Status == rpc.Corroborated || len(decision.Decision.Support) != 2 {
				t.Fatalf("invalid %s vote shrank fixed denominator: %+v, %v", edit.key, decision, err)
			}
			if !bytes.Equal(invalid, before) {
				t.Fatal("rejection mutated source evidence")
			}
		})
	}
}

// TestTransactionByHashCorrectionIdempotence pins the single-pass contract:
// every whole-key correction is idempotent over its own output, and repeated
// preparation of the same response yields the identical canonical string no
// matter how often it runs.
func TestTransactionByHashCorrectionIdempotence(t *testing.T) {
	_, raw, _ := observedPendingTransaction(t)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		once, matched, err := correctTransactionByHashKey(key, value, fields)
		if err != nil || !matched {
			continue
		}
		twice, again, err := correctTransactionByHashKey(key, once, fields)
		if err != nil {
			t.Fatalf("%s: canonicalized value rejected on re-entry: %v", key, err)
		}
		if again && string(once) != string(twice) {
			t.Fatalf("%s: correction is not idempotent: %s vs %s", key, once, twice)
		}
	}
	first, err := normalizeTransactionByHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := normalizeTransactionByHash(raw)
		if err != nil || again != first {
			t.Fatalf("repeated preparation diverged at round %d: %s, %v", i, again, err)
		}
	}
}
