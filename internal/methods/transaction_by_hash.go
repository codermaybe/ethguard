package methods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/ethobject/transaction"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

const MethodGetTransactionByHash = "eth_getTransactionByHash"

func transactionByHashSpec() MethodSpec {
	return MethodSpec{
		Method:    MethodGetTransactionByHash,
		Resolver:  ResolverStrict,
		Normalize: normalizeTransactionByHash,
		// KeySource is canonical-bytes: the comparison key is the encoder's
		// output. The normalizer returns exactly those bytes for this method,
		// so the clusters are unchanged — the declaration converges the
		// metadata with the processing route, which clusters on canonical
		// bytes by construction. The within-cluster encoding guard becomes
		// trivially true under this declaration; the commit kernel still
		// carries it for narrow-key methods.
		KeySource:              rpcengine.RuleKeyCanonicalBytes,
		CompareKind:            rpcengine.RuleCompareMethodSpecific,
		ProcessCanonical:       processTransactionByHash,
		ObservationProjectorID: ProjectorTransaction,
	}
}

func transactionByHashDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetTransactionByHash,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "TransactionByHash",
		Alignment: "v1.17.10",
	}
}

// processTransactionByHash is the method's canonical processing entry: one
// run prepares the candidate (safety gate, whole-key corrections over the
// read-only original, object representation work), binds it to the requested
// hash, and hands out the immutable canonical result. The raw normalizer and
// encoder are never consulted on this route — the canonical bytes are both
// the comparison key and the committed value. Evidence stays absent, exactly
// what the raw admission returns for this method.
func processTransactionByHash(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
	candidate, err := prepareTransactionCandidateByHash(value)
	if err != nil {
		return rpcengine.CanonicalResult{}, err
	}
	if err := candidate.AdmitByHash(req); err != nil {
		return rpcengine.CanonicalResult{}, err
	}
	return rpcengine.NewCanonicalResult(candidate.Canonical(), nil)
}

// prepareTransactionCandidateByHash is the by-hash preparation over the
// object kernel: the same safety gate, whole-key corrections, and unique
// missing-timestamp derivation as the raw preparation, ending in the
// candidate instead of re-entering Encode.
func prepareTransactionCandidateByHash(value json.RawMessage) (transaction.Candidate, error) {
	candidateRaw, err := correctTransactionByHash(value)
	if err != nil {
		return transaction.Candidate{}, err
	}
	return transaction.Prepare(candidateRaw)
}

func normalizeTransactionByHash(value json.RawMessage) (string, error) {
	candidate, err := prepareTransactionByHash(value)
	return string(candidate), err
}

// prepareTransactionByHash normalizes one provider response into the
// canonical candidate. The method's whole-key corrections run once per
// original official key; each key's state reads the original response only,
// and corrections write into an independent candidate so iteration order and
// repeated calls cannot change the canonical result.
func prepareTransactionByHash(value json.RawMessage) (json.RawMessage, error) {
	candidate, err := prepareTransactionCandidateByHash(value)
	if err != nil {
		return nil, err
	}
	return candidate.Canonical(), nil
}

// correctTransactionByHash is the method's whole-key correction pass: safety
// gate, root null, and the correction switch over the read-only original.
// The pass writes into an independent candidate; nothing re-enters it.
func correctTransactionByHash(value json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(value); err != nil {
		return nil, fmt.Errorf("%w: transaction JSON: %v", rpc.ErrMalformedResponse, err)
	}
	if ethcodec.IsJSONNull(value) {
		return json.RawMessage("null"), nil
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(value, &original); err != nil || original == nil {
		return nil, fmt.Errorf("%w: transaction must be an object", rpc.ErrMalformedResponse)
	}
	candidate := make(map[string]json.RawMessage, len(original)+1)
	for key, field := range original {
		corrected, matched, err := correctTransactionByHashKey(key, field, original)
		if err != nil {
			return nil, fmt.Errorf("%w: transaction %s: %v", rpc.ErrMalformedResponse, key, err)
		}
		if matched {
			candidate[key] = corrected
		} else {
			candidate[key] = field
		}
	}
	// The one approved missing-derived key: an absent blockTimestamp gets its
	// single state determination here, reading the original context only.
	// Present keys already had theirs in the pass above; nothing re-enters.
	if _, exists := original["blockTimestamp"]; !exists {
		corrected, matched, err := correctTransactionByHashKey("blockTimestamp", nil, original)
		if err != nil {
			return nil, fmt.Errorf("%w: transaction blockTimestamp: %v", rpc.ErrMalformedResponse, err)
		}
		if matched {
			candidate["blockTimestamp"] = corrected
		}
	}
	candidateRaw, err := ethcodec.MarshalNoEscape(candidate)
	if err != nil {
		return nil, fmt.Errorf("%w: transaction candidate: %v", rpc.ErrMalformedResponse, err)
	}
	return candidateRaw, nil
}

// correctTransactionByHashKey is the method's whole-key compatibility rule.
// The outer switch assigns each official direct key its owner; the inner
// switch distinguishes that key's complete input state. States read the
// original response only, and a key derived here (the pending blockTimestamp
// null) is written straight into the candidate — it never re-enters this
// pass. Missing required fields outside an approved derivation stay
// rejections by the official schema, attributed to their key.
func correctTransactionByHashKey(key string, value json.RawMessage, original map[string]json.RawMessage) (json.RawMessage, bool, error) {
	switch key {
	case "blockTimestamp":
		switch {
		case len(value) == 0:
			// Missing: only the explicit pending context derives the
			// complete null. Any other context leaves the absence
			// uncorrected — the official required field and the typed
			// branch then classify the response, keeping the unsupported
			// object's precedence over the missing member.
			if ethcodec.IsJSONNull(original["blockHash"]) && ethcodec.IsJSONNull(original["blockNumber"]) && ethcodec.IsJSONNull(original["transactionIndex"]) {
				return json.RawMessage("null"), true, nil
			}
			return nil, false, nil
		default:
			// Null and present keep the official value; the schema branch
			// owns nullability and grammar.
			return value, false, nil
		}
	case "hash", "blockHash":
		if key == "blockHash" && ethcodec.IsJSONNull(value) {
			return json.RawMessage("null"), true, nil
		}
		canonical, err := ethapi.NewHash(ethcodec.FoldHexSpelling(value))
		if err != nil {
			return nil, true, err
		}
		encoded, err := canonical.MarshalJSON()
		return encoded, true, err
	case "from", "to":
		if key == "to" && ethcodec.IsJSONNull(value) {
			return json.RawMessage("null"), true, nil
		}
		canonical, err := ethapi.NewAddress(ethcodec.FoldHexSpelling(value))
		if err != nil {
			return nil, true, err
		}
		encoded, err := canonical.MarshalJSON()
		return encoded, true, err
	case "input":
		encoded, err := ethcodec.EncodeData(value)
		return encoded, true, err
	default:
		return value, false, nil
	}
}
