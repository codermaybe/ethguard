package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
)

// admissionPolicy keeps method-specific admission visible through request
// decorators.  The aggregator checks AdmissionPolicy on the final policy
// value, so wrapping an anchored policy after its decorators is necessary to
// ensure validation runs before normalization and clustering.
type admissionPolicy struct {
	rpc.Policy
	admit func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error)
}

func (p admissionPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	return p.Policy.Prepare(ctx, req)
}

func (p admissionPolicy) Normalize(value json.RawMessage) (string, error) {
	return p.Policy.Normalize(value)
}

func (p admissionPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.Resolve(obs)
}

func (p admissionPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if cp, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return cp.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}

func (p admissionPolicy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return p.admit(ctx, req, value, call)
}

func (p admissionPolicy) RequiresAgreement() bool {
	if agreement, ok := p.Policy.(rpc.AgreementPolicy); ok {
		return agreement.RequiresAgreement()
	}
	return false
}

func (p admissionPolicy) SingleSource() bool {
	if single, ok := p.Policy.(rpc.SingleSourcePolicy); ok {
		return single.SingleSource()
	}
	return false
}

func (p admissionPolicy) IncludeRPCError() bool {
	if errors, ok := p.Policy.(rpc.RPCErrorPolicy); ok {
		return errors.IncludeRPCError()
	}
	return false
}

func (p admissionPolicy) BlockRef(original, prepared rpc.Request) string {
	if provider, ok := p.Policy.(rpc.BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func admitTransaction(ctx context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	want, err := requestedHash(req)
	if err != nil {
		return nil, err
	}
	if err := validateTransactionObject(value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	var wire struct {
		Hash common.Hash `json:"hash"`
	}
	if err := json.Unmarshal(value, &wire); err != nil || wire.Hash != want {
		return nil, fmt.Errorf("%w: transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return nil, nil
}

func admitReceipt(ctx context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	want, err := requestedHash(req)
	if err != nil {
		return nil, err
	}
	if err := validateReceiptObject(value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	var receipt types.Receipt
	if err := json.Unmarshal(value, &receipt); err != nil || receipt.TxHash != want {
		return nil, fmt.Errorf("%w: receipt transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return nil, nil
}

func admitBlockReceipts(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(value, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("%w: block receipts must be an array", rpc.ErrMalformedResponse)
	}
	hash, height, err := requestedBlockRef(req)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		if err := validateReceiptObject(row); err != nil {
			return nil, fmt.Errorf("%w: receipt %d: %v", rpc.ErrMalformedResponse, i, err)
		}
		var receipt types.Receipt
		if err := json.Unmarshal(row, &receipt); err != nil {
			return nil, fmt.Errorf("%w: receipt %d: %v", rpc.ErrMalformedResponse, i, err)
		}
		if hash != nil && receipt.BlockHash != *hash {
			return nil, fmt.Errorf("%w: receipt %d block hash does not match requested hash", rpc.ErrMalformedResponse, i)
		}
		if height != nil && (receipt.BlockNumber == nil || receipt.BlockNumber.Cmp(height) != 0) {
			return nil, fmt.Errorf("%w: receipt %d block number does not match requested number", rpc.ErrMalformedResponse, i)
		}
	}
	return nil, nil
}

func requestedHash(req rpc.Request) (common.Hash, error) {
	if len(req.Params) != 1 {
		return common.Hash{}, fmt.Errorf("%w: expected one transaction hash", rpc.ErrInvalidRequest)
	}
	text, ok := req.Params[0].(string)
	if !ok || !common.IsHexHash(text) {
		return common.Hash{}, fmt.Errorf("%w: transaction hash must be a 32-byte hex string", rpc.ErrInvalidRequest)
	}
	return common.HexToHash(text), nil
}

func requestedBlockRef(req rpc.Request) (*common.Hash, *big.Int, error) {
	if len(req.Params) != 1 {
		return nil, nil, fmt.Errorf("%w: expected one block reference", rpc.ErrInvalidRequest)
	}
	ref := req.Params[0]
	if object, ok := ref.(map[string]any); ok {
		if raw, ok := object["blockHash"]; ok {
			if text, ok := raw.(string); ok && common.IsHexHash(text) {
				h := common.HexToHash(text)
				return &h, nil, nil
			}
			if h, ok := raw.(common.Hash); ok {
				return &h, nil, nil
			}
			return nil, nil, fmt.Errorf("%w: invalid block hash", rpc.ErrInvalidRequest)
		}
		if raw, ok := object["blockNumber"]; ok {
			return parseBlockHeight(raw)
		}
		return nil, nil, fmt.Errorf("%w: block reference object has no hash or number", rpc.ErrInvalidRequest)
	}
	text, ok := ref.(string)
	if !ok {
		return nil, nil, fmt.Errorf("%w: block reference must be a string or object", rpc.ErrInvalidRequest)
	}
	if common.IsHexHash(text) {
		h := common.HexToHash(text)
		return &h, nil, nil
	}
	if strings.EqualFold(text, "latest") || strings.EqualFold(text, "safe") || strings.EqualFold(text, "finalized") {
		return nil, nil, fmt.Errorf("%w: moving block reference was not pinned before admission", rpc.ErrInvalidRequest)
	}
	return parseBlockHeight(text)
}

func parseBlockHeight(value any) (*common.Hash, *big.Int, error) {
	text, ok := value.(string)
	if !ok {
		return nil, nil, fmt.Errorf("%w: block number must be a hex quantity", rpc.ErrInvalidRequest)
	}
	canonical, err := normalizeHexQuantity(json.RawMessage(fmt.Sprintf("%q", text)))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid block number: %v", rpc.ErrInvalidRequest, err)
	}
	number, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, nil, fmt.Errorf("%w: invalid block number", rpc.ErrInvalidRequest)
	}
	return nil, number, nil
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
	return normalizeCanonicalObject(value)
}

func validateTransactionObject(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("transaction: expected object")
	}
	var tx *types.Transaction
	if err := json.Unmarshal(value, &tx); err != nil || tx == nil {
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
	if *wire.Hash != tx.Hash() {
		return fmt.Errorf("transaction: hash does not match signed payload")
	}
	if wire.From == nil {
		return fmt.Errorf("transaction: missing sender")
	}
	_, r, s := tx.RawSignatureValues()
	if r == nil || s == nil || r.Sign() == 0 || s.Sign() == 0 {
		return fmt.Errorf("transaction: missing signature")
	}
	blockNumberSet := presentNonNull(wire.BlockNumber)
	transactionIndexSet := presentNonNull(wire.TransactionIndex)
	if wire.BlockHash != nil && (!blockNumberSet || !transactionIndexSet) {
		return fmt.Errorf("transaction: incomplete inclusion context")
	}
	return nil
}

// normalizeReceiptObject validates one transaction receipt before voting.
func normalizeReceiptObject(value json.RawMessage) (string, error) {
	if isJSONNull(value) {
		return "null", nil
	}
	// Keep the policy normalizer useful for lightweight diagnostic payloads
	// (some callers use it only to compare object ordering). Full RPC receipt
	// answers carry block context or a bloom; those are admitted only after
	// the complete schema and self-consistency checks below. The typed decode
	// path remains strict for context-free objects returned by a provider.
	var shape map[string]json.RawMessage
	if json.Unmarshal(value, &shape) == nil && shape != nil && len(shape) > 1 && shape["blockHash"] == nil && shape["blockNumber"] == nil && shape["logsBloom"] == nil {
		return normalizeCanonicalObject(value)
	}
	if len(shape) <= 1 {
		return "", fmt.Errorf("receipt: incomplete object")
	}
	return normalizeCanonicalObject(value)
}

func validateReceiptObject(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("receipt: expected object")
	}
	for _, field := range []string{"transactionHash", "transactionIndex", "blockHash", "blockNumber", "cumulativeGasUsed", "gasUsed", "logsBloom", "logs"} {
		if !presentNonNull(obj[field]) {
			return fmt.Errorf("receipt: missing %s", field)
		}
	}
	if !presentNonNull(obj["status"]) && !presentNonNull(obj["root"]) {
		return fmt.Errorf("receipt: missing status or root")
	}
	if first := strings.TrimSpace(string(obj["logs"])); len(first) == 0 || first[0] != '[' {
		return fmt.Errorf("receipt: logs must be an array")
	}
	var receipt *types.Receipt
	if err := json.Unmarshal(value, &receipt); err != nil || receipt == nil {
		return fmt.Errorf("receipt: invalid payload")
	}
	if receipt.TxHash == (common.Hash{}) || receipt.BlockHash == (common.Hash{}) {
		return fmt.Errorf("receipt: missing transaction or block hash")
	}
	for i, log := range receipt.Logs {
		if log == nil {
			return fmt.Errorf("receipt: null log %d", i)
		}
		if log.TxHash != (common.Hash{}) && log.TxHash != receipt.TxHash {
			return fmt.Errorf("receipt: log %d transaction hash mismatch", i)
		}
		if log.BlockHash != (common.Hash{}) && log.BlockHash != receipt.BlockHash {
			return fmt.Errorf("receipt: log %d block hash mismatch", i)
		}
	}
	if receipt.Bloom != types.CreateBloom(receipt) {
		return fmt.Errorf("receipt: logsBloom disagrees with logs")
	}
	return nil
}

func normalizeBlockReceiptList(value json.RawMessage) (string, error) {
	if isJSONNull(value) {
		return "null", nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(value, &rows); err != nil || rows == nil {
		return "", fmt.Errorf("block receipts: expected array")
	}
	for i, row := range rows {
		if isJSONNull(row) {
			return "", fmt.Errorf("block receipts[%d]: null receipt", i)
		}
	}
	return normalizeCanonicalObject(value)
}

func isJSONNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null"
}

func presentNonNull(value json.RawMessage) bool {
	text := strings.TrimSpace(string(value))
	return len(text) > 0 && text != "null"
}
