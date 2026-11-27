// Package transaction contains the Ethereum transaction object rules shared by RPC methods.
package transaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

// Admit validates a transaction and checks it matches the requested hash.
// The raw entry over the object kernel: the request hash parse
// precedes any payload verdict, the object's payload validation runs once,
// and the declared hash must equal the requested hash. Method processors use
// Candidate.AdmitByHash on an already-prepared candidate instead of running
// this entry's preparation a second time.
func Admit(req rpc.Request, value json.RawMessage) error {
	if ethcodec.IsJSONNull(value) {
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
// the requested block hash and index. The raw entry over the
// object kernel: root null first, then the request grammar, then one object
// validation, then the inclusion binding. The old path's second full
// validation — the inner Admit re-running Validate on the same value — is
// retired: its requested hash was the value's own hash (unfailably parsed
// from its own Hex() spelling), its repeated validation could not disagree
// with the first, and its hash comparison bound the declared hash to itself.
// The declared-hash-versus-payload fact is verified once inside the object
// validation.
func AdmitInBlock(req rpc.Request, value json.RawMessage) error {
	if ethcodec.IsJSONNull(value) {
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
	if !ethcodec.PresentNonNull(wireValue.TransactionIndex) {
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

// Validate checks transaction payload, hash/signature, and inclusion context.
// The raw entry over the object kernel: the input must be an
// object, the representation work runs once through Prepare, and the payload
// and field checks read the candidate's views instead of re-encoding.
func Validate(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("transaction: expected object")
	}
	candidate, err := Prepare(value)
	if err != nil {
		return err
	}
	return validatePrepared(candidate)
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
		decimal, err := ethcodec.NormalizeWireQuantity(json.RawMessage(fmt.Sprintf("%q", v)))
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
