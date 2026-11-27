package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// CallContract returns the fleet-corroborated result of executing msg at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
// A fleet-corroborated revert is an observation, not a pipeline failure: it is
// returned as a *RevertError carrying the revert context, while the
// corroboration claim stays on the decision evidence.
func (c *Client) CallContract(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.callContractExec(opCtx, msg, blockNumber)
	})
}

func (c *Client) callContractExec(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) ([]byte, CallResult, error) {
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	call, err := toCallArg(msg)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, call, blockArg)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}

func (c *Client) CallContractAtHash(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.callContractAtHashExec(opCtx, msg, blockHash)
	})
}

func (c *Client) callContractAtHashExec(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	call, err := toCallArg(msg)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, call, hashBlockArg(blockHash))
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}

func (c *Client) PendingCallContract(ctx context.Context, msg ethapi.CallMsg) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) { return c.pendingCallContractExec(opCtx, msg) })
}

func (c *Client) pendingCallContractExec(ctx context.Context, msg ethapi.CallMsg) ([]byte, CallResult, error) {
	call, err := toCallArg(msg)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, call, "pending")
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}

// RevertError reports a fleet-corroborated execution revert of an eth_call.
// The corroborated observation is the data-aware comparison key (missing,
// null, empty, hex, or raw JSON). Message is the winning provider's own
// context and is carried separately from Decision.Value. None of the
// classified pipeline sentinels apply.
type RevertError struct {
	message string
	// Data is set only when the provider data is a hex string. Missing and
	// JSON null leave it nil; empty bytes ("0x") are a non-nil empty slice.
	Data []byte
	// DataRaw is the original JSON data value. Structured data stays here
	// and is not forced into hex. Missing data leaves DataRaw empty; JSON
	// null is retained as the bytes `null`.
	DataRaw json.RawMessage
}

func (e *RevertError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("ethguard: call reverted (corroborated observation): %s: 0x%x", e.message, e.Data)
	}
	return fmt.Sprintf("ethguard: call reverted (corroborated observation): %s", e.message)
}

// ErrorData makes RevertError compatible with go-ethereum's rpc.DataError.
// A hex string is returned unchanged. RevertError does not implement rpc.Error:
// go-ethereum's RevertErrorData helper also requires ErrorCode() == 3, so callers
// should use errors.As to read this error's Data or DataRaw directly.
// A structured value is returned as decoded JSON, never coerced to hex.
// Missing and JSON null return nil rather than an empty "0x", so absence
// is not presented as empty bytes.
func (e *RevertError) ErrorData() interface{} {
	if e == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(e.DataRaw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		if len(e.Data) > 0 {
			return hexutil.Encode(e.Data)
		}
		return nil
	}
	var value any
	if json.Unmarshal(trimmed, &value) == nil {
		return value
	}
	if len(e.Data) > 0 {
		return hexutil.Encode(e.Data)
	}
	return nil
}

// decodeCallDecision projects the corroborated call decision through the
// typed decoder and maps the neutral revert carrier onto the facade's own
// error type. internal/typed owns no facade error semantics.
func decodeCallDecision(decision rpc.Decision) ([]byte, error) {
	data, revert, err := typed.DecodeCallDecision(decision)
	if err != nil {
		return nil, err
	}
	if revert != nil {
		return nil, &RevertError{message: revert.Message, Data: revert.Data, DataRaw: revert.DataRaw}
	}
	return data, nil
}

// toCallArg validates the generated request model and freezes its complete
// official fields into ordinary JSON values before reference preparation.
func toCallArg(msg ethapi.CallMsg) (map[string]any, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	checked, err := ethapi.NewCallMsg(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	raw, err = json.Marshal(checked)
	if err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	var arg map[string]any
	// Open request members can contain arbitrary exact JSON numbers. The
	// generated parser has already checked this JSON; decoding it into
	// interface values must not round those members through float64.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&arg); err != nil {
		return nil, fmt.Errorf("%w: call message: %v", rpc.ErrInvalidRequest, err)
	}
	return arg, nil
}

// EstimateGas returns the fleet's lower-median gas estimate for msg at the
// currently corroborated reference — a variable result, Selected at best, never
// claimed corroborated. A fleet that unanimously fails estimation fails as
// no-valid-response; the revert is never laundered into a number.
func (c *Client) EstimateGas(ctx context.Context, msg ethapi.CallMsg) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.estimateGasExec(opCtx, msg)
	})
}

func (c *Client) estimateGasExec(ctx context.Context, msg ethapi.CallMsg) (uint64, CallResult, error) {
	call, err := toCallArg(msg)
	if err != nil {
		return 0, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodEstimateGas, call, "latest")
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	gas, err := typed.DecodeGasEstimate(decision.Value)
	return gas, CallResult{Decision: decisionOf(decision)}, err
}

func (c *Client) EstimateGasAtBlock(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.estimateGasAtBlockExec(opCtx, msg, blockNumber)
	})
}

// estimateGasAtBlockExec prepares the block argument and call object for
// both facades, then rides the shared estimate execution.
func (c *Client) estimateGasAtBlockExec(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) (uint64, CallResult, error) {
	arg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return 0, CallResult{}, err
	}
	call, err := toCallArg(msg)
	if err != nil {
		return 0, CallResult{}, err
	}
	return c.estimateGasWithArgExec(ctx, call, arg)
}

func (c *Client) EstimateGasAtBlockHash(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.estimateGasAtBlockHashExec(opCtx, msg, blockHash)
	})
}

// estimateGasAtBlockHashExec prepares the EIP-1898 hash object for both
// facades, then rides the shared estimate execution.
func (c *Client) estimateGasAtBlockHashExec(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) (uint64, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return 0, CallResult{}, err
	}
	call, err := toCallArg(msg)
	if err != nil {
		return 0, CallResult{}, err
	}
	return c.estimateGasWithArgExec(ctx, call, hashBlockArg(blockHash))
}

func (c *Client) estimateGasWithArgExec(ctx context.Context, call map[string]any, blockArg any) (uint64, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodEstimateGas, call, blockArg)
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	gas, err := typed.DecodeGasEstimate(decision.Value)
	return gas, CallResult{Decision: decisionOf(decision)}, err
}

// SuggestGasTipCap returns the fleet's lower-median priority-fee estimate —
// a variable result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodMaxPriorityFeePerGas)
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed corroborated.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodGasPrice)
}

// FeeHistory returns the fleet-corroborated fee history ending at the
// corroborated reference: lastBlock nil pins the moving "latest" tag, an
// explicit number passes through. The range is chain-determined once
// lastBlock is pinned, so the decision is Corroborated when sources agree;
// cross-client percentile rounding of Reward is the known liveness gate and
// fails closed as conflict until the soak measures it.
func (c *Client) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethapi.FeeHistoryResult, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.FeeHistoryResult, CallResult, error) {
		return c.feeHistoryExec(opCtx, blockCount, lastBlock, rewardPercentiles)
	})
}

func (c *Client) feeHistoryExec(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethapi.FeeHistoryResult, CallResult, error) {
	lastRef := "latest"
	if lastBlock != nil {
		lastRef = hexutil.EncodeBig(lastBlock)
	}
	decision, err := c.callTyped(ctx, methods.MethodFeeHistory, blockCount, lastRef, rewardPercentiles)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	history, projectErr := typed.DecodeFeeHistory(decision.Value)
	return history, CallResult{Decision: decisionOf(decision)}, projectErr
}

// BlobBaseFee returns the fleet's lower-median blob base fee (EIP-4844) — a
// variable result, so the decision is Selected at best, with outliers kept as
// evidence.
func (c *Client) BlobBaseFee(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodBlobBaseFee)
}

// CreateAccessList creates an EIP-2930 access list for a transaction at the
// corroborated reference block or an explicitly provided block argument.
// This EthGuard typed extension returns the access list alongside its decision
// evidence in CallResult.
func (c *Client) CreateAccessList(ctx context.Context, msg any, blockNrOrHash ...any) (*ethapi.AccessListResult, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.AccessListResult, CallResult, error) {
		params := []any{msg}
		if len(blockNrOrHash) > 0 {
			params = append(params, blockNrOrHash[0])
		}
		decision, err := c.callTyped(opCtx, methods.MethodCreateAccessList, params...)
		if err != nil {
			decision.Value = nil
			return nil, CallResult{Decision: decisionOf(decision)}, err
		}
		result, err := typed.DecodeAccessListResult(decision.Value)
		return result, CallResult{Decision: decisionOf(decision)}, err
	})
}
