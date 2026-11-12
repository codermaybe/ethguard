package methods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/transaction"
)

const MethodGetTransactionByBlockHashAndIndex = "eth_getTransactionByBlockHashAndIndex"

func transactionInBlockSpec() MethodSpec {
	return MethodSpec{
		Method:    MethodGetTransactionByBlockHashAndIndex,
		Resolver:  ResolverStrict,
		Normalize: normalizeTransactionInBlock,
		Encode:    prepareTransactionInBlock,
		// KeySource is canonical-bytes for the same convergence as by-hash:
		// the normalizer returns the encoder's bytes, so the clusters and
		// their ordering are unchanged while the declared key source now
		// names what the route actually compares.
		KeySource:              rpc.RuleKeyCanonicalBytes,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Admit:                  admitTransactionInBlock,
		ProcessCanonical:       processTransactionInBlock,
		ObservationProjectorID: ProjectorTransaction,
	}
}

func transactionInBlockDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetTransactionByBlockHashAndIndex,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "TransactionInBlock",
		Alignment: "v1.17.10",
	}
}

func admitTransactionInBlock(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	candidate, err := prepareTransactionInBlock(value)
	if err != nil {
		return nil, err
	}
	if err := transaction.AdmitInBlock(req, candidate); err != nil {
		return nil, err
	}
	return nil, nil
}

// processTransactionInBlock is the method's canonical processing entry. Its
// acceptance rides the shared object encoding and the official schema
// branches, with this method's mined-only condition: a transaction addressed
// by block hash and index is by definition included, so a null inclusion
// context cannot satisfy the request. No whole-key compatibility correction
// applies here — by-hash's pending timestamp derivation is not this method's
// rule and is not copied.
func processTransactionInBlock(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
	candidate, err := prepareTransactionCandidateInBlock(value)
	if err != nil {
		return rpc.CanonicalResult{}, err
	}
	if err := candidate.AdmitInBlock(req); err != nil {
		return rpc.CanonicalResult{}, err
	}
	return rpc.NewCanonicalResult(candidate.Canonical(), nil)
}

// prepareTransactionCandidateInBlock encodes the shared transaction candidate
// and rejects the pending shape before any request binding.
func prepareTransactionCandidateInBlock(value json.RawMessage) (transaction.Candidate, error) {
	candidate, err := transaction.Prepare(value)
	if err != nil {
		return transaction.Candidate{}, err
	}
	if candidate.Pending() {
		return transaction.Candidate{}, fmt.Errorf("%w: pending transaction has no requested block/index", rpc.ErrMalformedResponse)
	}
	return candidate, nil
}

func normalizeTransactionInBlock(value json.RawMessage) (string, error) {
	candidate, err := prepareTransactionInBlock(value)
	return string(candidate), err
}

// prepareTransactionInBlock is the raw route's preparation: the same
// candidate the processing entry builds, carrying its canonical bytes.
func prepareTransactionInBlock(value json.RawMessage) (json.RawMessage, error) {
	candidate, err := prepareTransactionCandidateInBlock(value)
	if err != nil {
		return nil, err
	}
	return candidate.Canonical(), nil
}
