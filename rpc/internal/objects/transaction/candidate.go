package transaction

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

// The object kernel. Encode's representation work — safety validation, the
// official-mask projection, the applicable generated-schema parse, the
// pending/mined consistency guard, and the canonical merge — is kept once as
// an immutable Candidate. The compatibility entries (Encode, Validate, Admit,
// AdmitInBlock) and the method processors all consume the same candidate, so
// the payload parse and the field/declaration checks run once per prepared
// object instead of once per phase that re-encodes.

// Candidate is one response's prepared representation. It is a processed
// value, not an admitted one: request binding happens only through the
// admission entries below, and a candidate carries no cross-call state —
// nothing here caches between invocations, and no candidate method mutates
// the receiver.
type Candidate struct {
	// canonical is the merged canonical bytes: typed values win, members the
	// official mask kept but the typed branch did not express are backfilled
	// from the official projection.
	canonical json.RawMessage
	// fields is the field view of the value Prepare consumed — the whole
	// official object including members outside the mask. The wire checks and
	// the admission bindings read it instead of unmarshaling the value again.
	fields map[string]json.RawMessage
}

// Prepare runs the object's representation work on one raw response: the
// bounded JSON-safety gate, the official-mask projection with its hex
// canonicalization, the applicable generated-schema parse (unsupported typed
// payloads keep their joined sentinel), the pending/mined consistency guard,
// and the canonical merge. The acceptance domain and the error order are
// Encode's — this function is Encode's body, retained verbatim as the kernel.
func Prepare(raw json.RawMessage) (Candidate, error) {
	canonical, err := encodeCanonical(raw)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{canonical: canonical, fields: fieldsOf(raw)}, nil
}

// Canonical returns a copy of the candidate's canonical bytes. The bytes are
// freshly built by Prepare and never aliased with the caller's input, and the
// copy keeps the candidate immutable when a caller writes to the returned
// slice: no read ever changes the candidate's later output.
func (c Candidate) Canonical() json.RawMessage {
	return append(json.RawMessage(nil), c.canonical...)
}

// Pending reports whether the candidate's inclusion context is a pending
// state: a non-null candidate whose blockHash is null. The object kernel does
// not act on pending by itself — whether a pending object is acceptable is
// the method's acceptance rule; the encode merge only guarantees the three
// inclusion members agree with each other.
func (c Candidate) Pending() bool {
	if c.fields == nil {
		return false
	}
	return IsJSONNull(c.fields["blockHash"])
}

// fields unmarshals the input value's field view. It runs only after the
// canonical encoding succeeded, so the value is already known to be a JSON
// object (or the root null, which reports no fields).
func fieldsOf(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	return fields
}

// validatePrepared is the object's one full payload validation: the
// go-ethereum payload parse of the canonical bytes, the declared-hash
// grammar and payload match, the declared sender grammar, the signature
// presence, the protected-legacy chainId declaration, and the inclusion
// context grammar. Every check reads the candidate's already-parsed views —
// no re-encode, no second schema parse. The checks keep Validate's order and
// its error classes: the caller joins rpc.ErrMalformedResponse where the
// compatibility entries always did.
func validatePrepared(c Candidate) error {
	var tx *types.Transaction
	if err := json.Unmarshal(c.canonical, &tx); err != nil || tx == nil {
		if errors.Is(err, types.ErrTxTypeNotSupported) {
			return errors.Join(rpc.ErrUnsupportedTypedObject, err)
		}
		return fmt.Errorf("transaction: invalid payload")
	}
	var wireValue struct {
		Hash             *common.Hash    `json:"hash"`
		From             *common.Address `json:"from"`
		BlockHash        *common.Hash    `json:"blockHash"`
		BlockNumber      json.RawMessage `json:"blockNumber"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(c.canonical, &wireValue); err != nil {
		return fmt.Errorf("transaction: invalid context: %w", err)
	}
	if wireValue.Hash == nil {
		return fmt.Errorf("transaction: missing hash")
	}
	if _, err := ethcodec.ValidateWireFixedHex(c.fields["hash"], 64, "hash"); err != nil {
		return fmt.Errorf("transaction: hash: %w", err)
	}
	if *wireValue.Hash != tx.Hash() {
		return fmt.Errorf("transaction: hash does not match signed payload")
	}
	if tx.Type() == types.LegacyTxType && tx.Protected() {
		if declared, exists := c.fields["chainId"]; exists {
			chainID, err := ethcodec.NormalizeWireQuantity(declared)
			if err != nil {
				return fmt.Errorf("transaction: chainId: %w", err)
			}
			if chainID != tx.ChainId().String() {
				return fmt.Errorf("transaction: chainId does not match signed payload")
			}
		}
	}
	if wireValue.From == nil {
		return fmt.Errorf("transaction: missing sender")
	}
	if _, err := ethcodec.ValidateWireFixedHex(c.fields["from"], 40, "from"); err != nil {
		return fmt.Errorf("transaction: from: %w", err)
	}
	_, r, s := tx.RawSignatureValues()
	if r == nil || s == nil || r.Sign() == 0 || s.Sign() == 0 {
		return fmt.Errorf("transaction: missing signature")
	}
	blockSet, indexSet := presentNonNull(wireValue.BlockNumber), presentNonNull(wireValue.TransactionIndex)
	if blockSet {
		if _, err := ethcodec.NormalizeWireQuantity(wireValue.BlockNumber); err != nil {
			return fmt.Errorf("transaction: blockNumber: %w", err)
		}
	}
	if indexSet {
		if _, err := ethcodec.NormalizeWireQuantity(wireValue.TransactionIndex); err != nil {
			return fmt.Errorf("transaction: transactionIndex: %w", err)
		}
	}
	if wireValue.BlockHash != nil {
		if _, err := ethcodec.ValidateWireFixedHex(c.fields["blockHash"], 64, "blockHash"); err != nil {
			return fmt.Errorf("transaction: blockHash: %w", err)
		}
	}
	if wireValue.BlockHash != nil && (!blockSet || !indexSet) {
		return fmt.Errorf("transaction: incomplete inclusion context")
	}
	return nil
}

// AdmitByHash binds a prepared candidate to a by-hash request. The order is
// the compatibility Admit's: a root null needs no binding, a request error
// precedes any payload verdict, the object's payload validation runs once,
// and the declared hash must equal the requested hash. The candidate is not
// mutated and no successful value is produced — binding success is the
// method processor's signal to commit the canonical bytes.
func (c Candidate) AdmitByHash(req rpc.Request) error {
	if IsJSONNull(c.canonical) {
		return nil
	}
	want, err := requestedHash(req)
	if err != nil {
		return err
	}
	if err := validatePrepared(c); err != nil {
		return errors.Join(rpc.ErrMalformedResponse, err)
	}
	var shape struct {
		Hash common.Hash `json:"hash"`
	}
	if err := json.Unmarshal(c.canonical, &shape); err != nil || shape.Hash != want {
		return fmt.Errorf("%w: transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return nil
}

// AdmitInBlock binds a prepared candidate to a block-hash-and-index request.
// The order is the compatibility AdmitInBlock's: root null first, then the
// request grammar, then the object's payload validation once, then the
// inclusion binding. The compatibility path's second full validation — the
// inner Admit re-running Validate on the same value — is gone: the declared-
// hash-versus-payload check inside validatePrepared is the same fact the
// inner round re-verified, and the requested-hash binding of this shape is
// the block hash and index comparison below.
func (c Candidate) AdmitInBlock(req rpc.Request) error {
	if IsJSONNull(c.canonical) {
		return nil
	}
	if len(req.Params) != 2 {
		return fmt.Errorf("%w: expected block hash and transaction index", rpc.ErrInvalidRequest)
	}
	wantHash, err := requestBlockHash(req.Params[0])
	if err != nil {
		return err
	}
	wantIndex, err := requestIndex(req.Params[1])
	if err != nil {
		return err
	}
	if err := validatePrepared(c); err != nil {
		return errors.Join(rpc.ErrMalformedResponse, err)
	}
	var wireValue struct {
		Hash             *common.Hash    `json:"hash"`
		BlockHash        *common.Hash    `json:"blockHash"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(c.canonical, &wireValue); err != nil {
		return fmt.Errorf("%w: transaction context: %v", rpc.ErrMalformedResponse, err)
	}
	if wireValue.Hash == nil || wireValue.BlockHash == nil || *wireValue.BlockHash != wantHash {
		return fmt.Errorf("%w: transaction block hash does not match requested block", rpc.ErrMalformedResponse)
	}
	if !presentNonNull(wireValue.TransactionIndex) {
		return fmt.Errorf("%w: transaction missing index", rpc.ErrMalformedResponse)
	}
	gotIndex, err := ethcodec.NormalizeWireQuantity(wireValue.TransactionIndex)
	if err != nil {
		return fmt.Errorf("%w: transaction index: %v", rpc.ErrMalformedResponse, err)
	}
	if gotIndex != fmt.Sprintf("%d", wantIndex) {
		return fmt.Errorf("%w: transaction index does not match requested index", rpc.ErrMalformedResponse)
	}
	return nil
}
