package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/transaction"
)

// The baseline witnesses for the two transaction RPCs: the request-binding
// error classes, the JSON-safety rejection, the single-pass preparation
// contract on the methods layer, and the object-level inclusion-context
// guard. These pin the current behavior the processing refactor must
// reproduce; they do not authorize any acceptance-domain change.

func TestTransactionRequestBindingErrorClasses(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	valid := raw
	byHash := func(params []any) error {
		_, err := admitTransactionByHash(context.Background(), rpc.Request{Method: MethodGetTransactionByHash, Params: params}, valid, nil)
		return err
	}
	for name, params := range map[string][]any{
		"no-params":       {},
		"two-params":      {txHash(t, raw), txHash(t, raw)},
		"non-string-hash": {42},
		"short-hash":      {"0x1234"},
		"non-hex-hash":    {"0x" + string(bytes.Repeat([]byte("zz"), 32))},
	} {
		t.Run("by-hash/"+name, func(t *testing.T) {
			if err := byHash(params); !errors.Is(err, rpc.ErrInvalidRequest) {
				t.Fatalf("by-hash request binding = %v, want ErrInvalidRequest", err)
			}
		})
	}
	inBlock := func(params []any) error {
		_, err := admitTransactionInBlock(context.Background(), rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: params}, valid, nil)
		return err
	}
	blockHash := `"0x` + string(bytes.Repeat([]byte("aa"), 32)) + `"`
	for name, params := range map[string][]any{
		"no-params":           {},
		"one-param":           {json.RawMessage(blockHash)},
		"three-params":        {json.RawMessage(blockHash), "0x0", "extra"},
		"non-string-block":    {42, "0x0"},
		"non-hex-block":       {"0x" + string(bytes.Repeat([]byte("zz"), 32)), "0x0"},
		"unsupported-index":   {json.RawMessage(blockHash), true},
		"object-index":        {json.RawMessage(blockHash), map[string]any{}},
		"decimal-index":       {json.RawMessage(blockHash), "1"},
		"malformed-index-str": {json.RawMessage(blockHash), "zz"},
	} {
		t.Run("in-block/"+name, func(t *testing.T) {
			if err := inBlock(params); !errors.Is(err, rpc.ErrInvalidRequest) {
				t.Fatalf("in-block request binding = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

// TestTransactionAdmissionMatchesCompatChain pins the admission entries'
// preparation-once binding against the retired chain — prepare the response,
// then run the compatibility Admit/AdmitInBlock entry on the prepared
// canonical bytes. Verdicts and full sentinel sets must not drift on the
// official and recorded fixture corpus, and each direction must exercise a
// real acceptance, so a silently both-failed round cannot pass.
func TestTransactionAdmissionMatchesCompatChain(t *testing.T) {
	accepts := map[string]int{}
	for _, raw := range transactionProcessorFixtures(t) {
		requested, hasHash := declaredHash(t, raw)
		var inclusion struct {
			BlockHash        *string `json:"blockHash"`
			TransactionIndex *string `json:"transactionIndex"`
		}
		_ = json.Unmarshal(raw, &inclusion)
		byHash := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{requested}}
		if !hasHash {
			byHash.Params = []any{"0x" + string(bytes.Repeat([]byte("11"), 32))}
		}
		wrongHash := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{"0x" + string(bytes.Repeat([]byte("22"), 32))}}
		badGrammar := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{"zz"}}
		type binding struct {
			name    string
			request rpc.Request
		}
		requests := []binding{
			{"by-hash/declared", byHash},
			{"by-hash/wrong", wrongHash},
			{"by-hash/bad-grammar", badGrammar},
		}
		if inclusion.BlockHash != nil && inclusion.TransactionIndex != nil {
			requests = append(requests,
				binding{"in-block/declared", rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{*inclusion.BlockHash, *inclusion.TransactionIndex}}},
				binding{"in-block/wrong-block", rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{"0x" + string(bytes.Repeat([]byte("33"), 32)), *inclusion.TransactionIndex}}},
				binding{"in-block/bad-index", rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{*inclusion.BlockHash, "zz"}}},
			)
		}
		prepared, prepareErr := prepareTransactionByHash(raw)
		nullShape := prepareErr == nil && bytes.Equal(bytes.TrimSpace(prepared), []byte("null"))
		for _, item := range requests {
			var retiredErr, boundErr error
			switch item.request.Method {
			case MethodGetTransactionByHash:
				canonical, err := prepareTransactionByHash(raw)
				if err == nil {
					retiredErr = transaction.Admit(item.request, canonical)
				} else {
					retiredErr = err
				}
				_, boundErr = admitTransactionByHash(context.Background(), item.request, raw, nil)
			default:
				canonical, err := prepareTransactionInBlock(raw)
				if err == nil {
					retiredErr = transaction.AdmitInBlock(item.request, canonical)
				} else {
					retiredErr = err
				}
				_, boundErr = admitTransactionInBlock(context.Background(), item.request, raw, nil)
			}
			if retirementSentinels(retiredErr) != retirementSentinels(boundErr) {
				t.Fatalf("%s sentinel sets differ: retired=%q (%v) bound=%q (%v)",
					item.name, retirementSentinels(retiredErr), retiredErr, retirementSentinels(boundErr), boundErr)
			}
			accepts[item.name]++
			rejection := strings.HasSuffix(item.name, "wrong") || strings.HasSuffix(item.name, "bad-grammar") ||
				strings.HasSuffix(item.name, "wrong-block") || strings.HasSuffix(item.name, "bad-index")
			if rejection && boundErr == nil && !nullShape {
				t.Fatalf("%s accepted a rejected-direction request on a non-null fixture", item.name)
			}
		}
	}
	for _, name := range []string{"by-hash/declared", "in-block/declared"} {
		if accepts[name] == 0 {
			t.Fatalf("%s never exercised a real acceptance", name)
		}
	}
}

// retirementSentinels renders every sentinel the error carries so an
// Unsupported+Malformed join never compares equal to a bare class.
func retirementSentinels(err error) string {
	if err == nil {
		return "ok"
	}
	names := ""
	for _, item := range []struct {
		name string
		sent error
	}{
		{"invalid-request", rpc.ErrInvalidRequest},
		{"malformed", rpc.ErrMalformedResponse},
		{"unsupported", rpc.ErrUnsupportedTypedObject},
	} {
		if errors.Is(err, item.sent) {
			names += item.name + "+"
		}
	}
	if names == "" {
		return "other"
	}
	return names
}

// TestTransactionJSONSafetyRejectsDuplicateKeys pins the bounded JSON-safety
// gate: a response with a duplicate object key is rejected before any
// correction or encoding, on both methods' preparation paths.
func TestTransactionJSONSafetyRejectsDuplicateKeys(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	duplicated := `{"hash":"` + request.Params[0].(string) + `","hash":"0x1"}`
	for name, prepare := range map[string]func(json.RawMessage) (json.RawMessage, error){
		"by-hash":  prepareTransactionByHash,
		"in-block": prepareTransactionInBlock,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepare(json.RawMessage(duplicated)); !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("duplicate key response = %v, want ErrMalformedResponse", err)
			}
		})
	}
	// The valid fixture still prepares: the gate is input-shape scoped, not a
	// blanket rule change.
	if _, err := prepareTransactionByHash(raw); err != nil {
		t.Fatal(err)
	}
}

// TestTransactionPreparationIsIdempotentOnCanonicalInput pins the chained
// single-pass contract on the methods layer: preparing an already-prepared
// canonical candidate yields the same bytes, for both methods. The refactor
// that stops re-running full preparation must keep this equivalence for any
// consumer that still re-enters the preparation entry.
func TestTransactionPreparationIsIdempotentOnCanonicalInput(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	once, err := prepareTransactionByHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := prepareTransactionByHash(once)
	if err != nil || !bytes.Equal(once, twice) {
		t.Fatalf("by-hash prepare(prepare(raw)) = %s, %v; want %s", twice, err, once)
	}
	// In-block preparation is idempotent on its mined shape: the same fixture
	// with its inclusion context intact re-prepares byte-identically.
	mined := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	requested, err := prepareTransactionInBlock(mined)
	if err != nil {
		t.Fatal(err)
	}
	again, err := prepareTransactionInBlock(requested)
	if err != nil || !bytes.Equal(requested, again) {
		t.Fatalf("in-block prepare(prepare(raw)) = %s, %v; want %s", again, err, requested)
	}
	// The chain holds from the pending fixture too: the derived blockTimestamp
	// null is stable under re-entry.
	pendingOnce, err := prepareTransactionByHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	pendingTwice, err := prepareTransactionByHash(pendingOnce)
	if err != nil || !bytes.Equal(pendingOnce, pendingTwice) {
		t.Fatalf("pending derivation not stable under re-entry: %v", err)
	}
	if _, err := admitTransactionByHash(context.Background(), request, pendingTwice, nil); err != nil {
		t.Fatalf("prepared candidate re-admission = %v", err)
	}
}

// TestTransactionInclusionContextGuardPerAsymmetricMember exercises the
// encoder's pending/mined consistency guard directly, one asymmetric member
// at a time: any single inclusion member disagreeing with the null state of
// the others rejects, independent of the by-hash derivation order.
func TestTransactionInclusionContextGuardPerAsymmetricMember(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	minedValue := map[string]string{
		"blockHash":        `"` + request.Params[0].(string) + `"`,
		"blockNumber":      `"0x1"`,
		"transactionIndex": `"0x0"`,
	}
	for _, key := range []string{"blockHash", "blockNumber", "transactionIndex"} {
		t.Run(key+"/set-in-pending", func(t *testing.T) {
			mutated := injectField(t, raw, key, minedValue[key])
			if _, err := prepareTransactionByHash(mutated); !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("pending context with mined %s was accepted: %v", key, err)
			}
		})
	}
	// Reverse direction: a mined context with one member nulled rejects.
	mined := raw
	for key, value := range minedValue {
		mined = injectField(t, mined, key, value)
	}
	mined = injectField(t, mined, "blockTimestamp", `"0x5"`)
	for _, key := range []string{"blockHash", "blockNumber", "transactionIndex"} {
		t.Run(key+"/null-in-mined", func(t *testing.T) {
			mutated := injectField(t, mined, key, `null`)
			if _, err := prepareTransactionByHash(mutated); !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("mined context with null %s was accepted: %v", key, err)
			}
		})
	}
}

// TestTransactionMissingSignatureFieldsReject pins the signature-presence
// branch: a well-formed payload whose response omits the signature members,
// the hash, or the sender is an admission failure attributed to that member,
// not a comparison key.
func TestTransactionMissingSignatureFieldsReject(t *testing.T) {
	request, raw, _ := observedPendingTransaction(t)
	for _, key := range []string{"r", "s", "v", "hash", "from"} {
		t.Run(key, func(t *testing.T) {
			missing := deleteField(t, raw, key)
			_, err := admitTransactionByHash(context.Background(), request, missing, nil)
			if !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("absent %s admitted: %v", key, err)
			}
			if _, err := normalizeTransactionByHash(missing); err == nil {
				t.Fatalf("absent %s contributed a comparison key", key)
			}
		})
	}
}

// TestTransactionInBlockPendingRejectionMessage pins the exact class and
// message of the in-block pending rejection: the mined-only acceptance is a
// malformed-response fact carrying its own explanation, not a generic
// not-found.
func TestTransactionInBlockPendingRejectionMessage(t *testing.T) {
	pending := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		pending = injectField(t, pending, key, "null")
	}
	_, err := prepareTransactionInBlock(pending)
	if !errors.Is(err, rpc.ErrMalformedResponse) || err.Error() != "rpc: malformed response: pending transaction has no requested block/index" {
		t.Fatalf("in-block pending rejection = %v, want the pinned malformed-response message", err)
	}
}
