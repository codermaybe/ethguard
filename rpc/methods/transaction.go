package methods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
)

// This file is the Transaction object slice: the Transaction shape table,
// its semantic gates (hash/signature/inclusion context), and its projection,
// owned by eth_getTransactionByHash (R2: this slice solely owns the
// Transaction table; the shared admissionPolicy decorator and request/JSON
// helpers live in receipt.go until prune.go absorbs them).

// MethodSendRawTransaction is the write-side JSON-RPC method. It has no
// Policy in the read registry: the write pipeline owns its collection and
// adjudication (pool.Broadcast + relative-trust resolution), not the
// method-policy registry. The constant lives here as the transaction
// namespace's method name until the write side gets its own home.
const MethodSendRawTransaction = "eth_sendRawTransaction"

func admitTransaction(ctx context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	want, err := requestedHash(req)
	if err != nil {
		return nil, err
	}
	if err := validateTransactionObject(value); err != nil {
		return nil, errors.Join(rpc.ErrMalformedResponse, err)
	}
	var wire struct {
		Hash common.Hash `json:"hash"`
	}
	if err := json.Unmarshal(value, &wire); err != nil || wire.Hash != want {
		return nil, fmt.Errorf("%w: transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return nil, nil
}

// normalizeTransactionObject admits only a complete transaction object (or
// the protocol's null absence) before it can enter a voting cluster.  The
// transaction hash is checked against the signed payload; placement fields
// are checked as a group so a mined object cannot carry a half-populated
// inclusion context.
func normalizeTransactionObject(value json.RawMessage) (string, error) {
	if isJSONNull(value) {
		return "null", nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(value, &shape); err != nil || shape == nil {
		return "", fmt.Errorf("transaction: expected object")
	}
	if len(shape) <= 1 {
		return "", fmt.Errorf("transaction: incomplete object")
	}
	return PruneMethod(MethodGetTransactionByHash, value)
}

func validateTransactionObject(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("transaction: expected object")
	}
	var tx *types.Transaction
	if err := json.Unmarshal(value, &tx); err != nil || tx == nil {
		if errors.Is(err, types.ErrTxTypeNotSupported) {
			return errors.Join(rpc.ErrUnsupportedTypedObject, err)
		}
		return fmt.Errorf("transaction: invalid payload")
	}
	var wire struct {
		Hash             *common.Hash    `json:"hash"`
		From             *common.Address `json:"from"`
		BlockHash        *common.Hash    `json:"blockHash"`
		BlockNumber      json.RawMessage `json:"blockNumber"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(value, &wire); err != nil {
		return fmt.Errorf("transaction: invalid context: %w", err)
	}
	if wire.Hash == nil {
		return fmt.Errorf("transaction: missing hash")
	}
	if _, err := validateWireFixedHex(obj["hash"], 64, "hash"); err != nil {
		return fmt.Errorf("transaction: hash: %w", err)
	}
	if *wire.Hash != tx.Hash() {
		return fmt.Errorf("transaction: hash does not match signed payload")
	}
	if wire.From == nil {
		return fmt.Errorf("transaction: missing sender")
	}
	if _, err := validateWireFixedHex(obj["from"], 40, "from"); err != nil {
		return fmt.Errorf("transaction: from: %w", err)
	}
	_, r, s := tx.RawSignatureValues()
	if r == nil || s == nil || r.Sign() == 0 || s.Sign() == 0 {
		return fmt.Errorf("transaction: missing signature")
	}
	blockNumberSet := presentNonNull(wire.BlockNumber)
	transactionIndexSet := presentNonNull(wire.TransactionIndex)
	if blockNumberSet {
		if _, err := normalizeWireQuantity(wire.BlockNumber); err != nil {
			return fmt.Errorf("transaction: blockNumber: %w", err)
		}
	}
	if transactionIndexSet {
		if _, err := normalizeWireQuantity(wire.TransactionIndex); err != nil {
			return fmt.Errorf("transaction: transactionIndex: %w", err)
		}
	}
	if wire.BlockHash != nil {
		if _, err := validateWireFixedHex(obj["blockHash"], 64, "blockHash"); err != nil {
			return fmt.Errorf("transaction: blockHash: %w", err)
		}
	}
	if wire.BlockHash != nil && (!blockNumberSet || !transactionIndexSet) {
		return fmt.Errorf("transaction: incomplete inclusion context")
	}
	return nil
}
