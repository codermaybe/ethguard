// Package transaction contains the Ethereum transaction object rules shared by RPC methods.
package transaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// Pruner removes fields outside a method's comparison shape.
type Pruner func(method string, raw json.RawMessage) (string, error)

// IsJSONNull and presentNonNull forward to the shared wire spellings: the
// former local copy skipped whitespace trimming, so " null " read as an
// object; every object package now answers the null/presence question
// identically.
func IsJSONNull(value json.RawMessage) bool { return wire.IsJSONNull(value) }
func presentNonNull(value json.RawMessage) bool {
	return wire.PresentNonNull(value)
}

// Admit validates a transaction and checks it matches the requested hash.
func Admit(req rpc.Request, value json.RawMessage) error {
	if IsJSONNull(value) {
		return nil
	}
	want, err := requestedHash(req)
	if err != nil {
		return err
	}
	if err := Validate(value); err != nil {
		return errors.Join(rpc.ErrMalformedResponse, err)
	}
	var shape struct {
		Hash common.Hash `json:"hash"`
	}
	if err := json.Unmarshal(value, &shape); err != nil || shape.Hash != want {
		return fmt.Errorf("%w: transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return nil
}

// AdmitInBlock validates a transaction returned by
// eth_getTransactionByBlockHashAndIndex and binds its inclusion metadata to
// the requested block hash and index.
func AdmitInBlock(req rpc.Request, value json.RawMessage) error {
	if IsJSONNull(value) {
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
	if err := Validate(value); err != nil {
		return errors.Join(rpc.ErrMalformedResponse, err)
	}
	var wireValue struct {
		Hash             *common.Hash    `json:"hash"`
		BlockHash        *common.Hash    `json:"blockHash"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(value, &wireValue); err != nil {
		return fmt.Errorf("%w: transaction context: %v", rpc.ErrMalformedResponse, err)
	}
	if wireValue.Hash == nil || wireValue.BlockHash == nil || *wireValue.BlockHash != wantHash {
		return fmt.Errorf("%w: transaction block hash does not match requested block", rpc.ErrMalformedResponse)
	}
	if !presentNonNull(wireValue.TransactionIndex) {
		return fmt.Errorf("%w: transaction missing index", rpc.ErrMalformedResponse)
	}
	gotIndex, err := wire.NormalizeWireQuantity(wireValue.TransactionIndex)
	if err != nil {
		return fmt.Errorf("%w: transaction index: %v", rpc.ErrMalformedResponse, err)
	}
	if gotIndex != fmt.Sprintf("%d", wantIndex) {
		return fmt.Errorf("%w: transaction index does not match requested index", rpc.ErrMalformedResponse)
	}
	if err := Admit(rpc.Request{Method: "eth_getTransactionByHash", Params: []any{wireValue.Hash.Hex()}}, value); err != nil {
		return err
	}
	return nil
}

// Normalize chooses the comparison key. It rejects only values with no
// canonical object form. Required fields and hash binding belong to Admit.
func Normalize(method string, value json.RawMessage, pruner Pruner) (string, error) {
	if IsJSONNull(value) {
		return "null", nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(value, &shape); err != nil || shape == nil {
		return "", fmt.Errorf("transaction: expected object")
	}
	if len(shape) <= 1 {
		return "", fmt.Errorf("transaction: incomplete object")
	}
	return pruner(method, value)
}

// NormalizeInBlock uses the same transaction field mask as the by-hash
// method; admission above supplies the additional block/index binding.
func NormalizeInBlock(value json.RawMessage, pruner Pruner) (string, error) {
	return Normalize("eth_getTransactionByHash", value, pruner)
}

// Validate checks transaction payload, hash/signature, and inclusion context.
func Validate(value json.RawMessage) error {
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
	var wireValue struct {
		Hash             *common.Hash    `json:"hash"`
		From             *common.Address `json:"from"`
		BlockHash        *common.Hash    `json:"blockHash"`
		BlockNumber      json.RawMessage `json:"blockNumber"`
		TransactionIndex json.RawMessage `json:"transactionIndex"`
	}
	if err := json.Unmarshal(value, &wireValue); err != nil {
		return fmt.Errorf("transaction: invalid context: %w", err)
	}
	if wireValue.Hash == nil {
		return fmt.Errorf("transaction: missing hash")
	}
	if _, err := wire.ValidateWireFixedHex(obj["hash"], 64, "hash"); err != nil {
		return fmt.Errorf("transaction: hash: %w", err)
	}
	if *wireValue.Hash != tx.Hash() {
		return fmt.Errorf("transaction: hash does not match signed payload")
	}
	if wireValue.From == nil {
		return fmt.Errorf("transaction: missing sender")
	}
	if _, err := wire.ValidateWireFixedHex(obj["from"], 40, "from"); err != nil {
		return fmt.Errorf("transaction: from: %w", err)
	}
	_, r, s := tx.RawSignatureValues()
	if r == nil || s == nil || r.Sign() == 0 || s.Sign() == 0 {
		return fmt.Errorf("transaction: missing signature")
	}
	blockSet, indexSet := presentNonNull(wireValue.BlockNumber), presentNonNull(wireValue.TransactionIndex)
	if blockSet {
		if _, err := wire.NormalizeWireQuantity(wireValue.BlockNumber); err != nil {
			return fmt.Errorf("transaction: blockNumber: %w", err)
		}
	}
	if indexSet {
		if _, err := wire.NormalizeWireQuantity(wireValue.TransactionIndex); err != nil {
			return fmt.Errorf("transaction: transactionIndex: %w", err)
		}
	}
	if wireValue.BlockHash != nil {
		if _, err := wire.ValidateWireFixedHex(obj["blockHash"], 64, "blockHash"); err != nil {
			return fmt.Errorf("transaction: blockHash: %w", err)
		}
	}
	if wireValue.BlockHash != nil && (!blockSet || !indexSet) {
		return fmt.Errorf("transaction: incomplete inclusion context")
	}
	return nil
}

func requestedHash(req rpc.Request) (common.Hash, error) {
	var hash common.Hash
	if len(req.Params) != 1 {
		return hash, fmt.Errorf("%w: expected one transaction hash", rpc.ErrInvalidRequest)
	}
	s, ok := req.Params[0].(string)
	if !ok || !common.IsHexHash(s) {
		return hash, fmt.Errorf("%w: transaction hash must be a 32-byte hex string", rpc.ErrInvalidRequest)
	}
	return common.HexToHash(s), nil
}

func requestBlockHash(value any) (common.Hash, error) {
	s, ok := value.(string)
	if !ok || !common.IsHexHash(s) {
		return common.Hash{}, fmt.Errorf("%w: block hash must be a 32-byte hex string", rpc.ErrInvalidRequest)
	}
	return common.HexToHash(s), nil
}

func requestIndex(value any) (uint64, error) {
	switch v := value.(type) {
	case uint:
		return uint64(v), nil
	case uint64:
		return v, nil
	case hexutil.Uint64:
		return uint64(v), nil
	case json.Number:
		index, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: transaction index: %v", rpc.ErrInvalidRequest, err)
		}
		return index, nil
	case string:
		decimal, err := wire.NormalizeWireQuantity(json.RawMessage(fmt.Sprintf("%q", v)))
		if err != nil {
			return 0, fmt.Errorf("%w: transaction index: %v", rpc.ErrInvalidRequest, err)
		}
		var out uint64
		if _, err := fmt.Sscanf(decimal, "%d", &out); err != nil {
			return 0, fmt.Errorf("%w: transaction index: %v", rpc.ErrInvalidRequest, err)
		}
		return out, nil
	default:
		return 0, fmt.Errorf("%w: transaction index has unsupported type %T", rpc.ErrInvalidRequest, value)
	}
}
