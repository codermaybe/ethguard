package ethadapter

import (
	"context"
	"math/big"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// CallContract converts the call input and executes it through core.
func (c *Client) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	input, report, err := FromGethCallMsg(msg)
	return execute(c, ctx, "CallContract", report, err, func() ([]byte, ethguard.CallResult, error) { return c.core.CallContract(ctx, input, blockNumber) }, identity[[]byte])
}

// CallContractAtHash pins the call to an immutable block hash through core.
func (c *Client) CallContractAtHash(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) ([]byte, error) {
	input, report, err := FromGethCallMsg(msg)
	return execute(c, ctx, "CallContractAtHash", report, err, func() ([]byte, ethguard.CallResult, error) {
		return c.core.CallContractAtHash(ctx, input, hash(blockHash))
	}, identity[[]byte])
}

// PendingCallContract keeps core's node-local pending call semantics.
func (c *Client) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	input, report, err := FromGethCallMsg(msg)
	return execute(c, ctx, "PendingCallContract", report, err, func() ([]byte, ethguard.CallResult, error) { return c.core.PendingCallContract(ctx, input) }, identity[[]byte])
}

// EstimateGas executes a gas estimate through core's reference pipeline.
func (c *Client) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	input, report, err := FromGethCallMsg(msg)
	return execute(c, ctx, "EstimateGas", report, err, func() (uint64, ethguard.CallResult, error) { return c.core.EstimateGas(ctx, input) }, identity[uint64])
}

// EstimateGasAtBlock estimates against the requested block through core.
func (c *Client) EstimateGasAtBlock(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) (uint64, error) {
	input, report, err := FromGethCallMsg(msg)
	return execute(c, ctx, "EstimateGasAtBlock", report, err, func() (uint64, ethguard.CallResult, error) { return c.core.EstimateGasAtBlock(ctx, input, blockNumber) }, identity[uint64])
}

// EstimateGasAtBlockHash estimates against an immutable hash through core.
func (c *Client) EstimateGasAtBlockHash(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) (uint64, error) {
	input, report, err := FromGethCallMsg(msg)
	return execute(c, ctx, "EstimateGasAtBlockHash", report, err, func() (uint64, ethguard.CallResult, error) {
		return c.core.EstimateGasAtBlockHash(ctx, input, hash(blockHash))
	}, identity[uint64])
}

// FilterLogs preserves core's range and reorganization guarantees.
func (c *Client) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	input, report, err := FromGethFilterQuery(q)
	return execute(c, ctx, "FilterLogs", report, err, func() ([]ethapi.Log, ethguard.CallResult, error) { return c.core.FilterLogs(ctx, input) }, func(logs []ethapi.Log, _ ethguard.CallResult) ([]types.Log, ConversionReport, error) {
		out := make([]types.Log, len(logs))
		report := ConversionReport{}
		for i, l := range logs {
			v, item, err := ToGethLog(l)
			report.merge(item)
			if err != nil {
				return nil, report, err
			}
			out[i] = v
		}
		return out, report, nil
	})
}

// BlockReceipts fetches the complete receipt set through core.
func (c *Client) BlockReceipts(ctx context.Context, ref gethrpc.BlockNumberOrHash) ([]*types.Receipt, error) {
	input, err := fromBlockReference(ref)
	return execute(c, ctx, "BlockReceipts", ConversionReport{}, err, func() ([]ethapi.Receipt, ethguard.CallResult, error) { return c.core.BlockReceipts(ctx, input) }, func(rows []ethapi.Receipt, _ ethguard.CallResult) ([]*types.Receipt, ConversionReport, error) {
		out := make([]*types.Receipt, len(rows))
		report := ConversionReport{}
		for i := range rows {
			v, item, err := ToGethReceipt(&rows[i])
			report.merge(item)
			if err != nil {
				return nil, report, err
			}
			out[i] = v
		}
		return out, report, nil
	})
}

// SyncProgress converts the standard counters and reports unavailable geth-specific fields.
func (c *Client) SyncProgress(ctx context.Context) (*ethereum.SyncProgress, error) {
	return execute(c, ctx, "SyncProgress", ConversionReport{}, nil, func() (*ethapi.SyncProgress, ethguard.CallResult, error) { return c.core.SyncProgress(ctx) }, syncProgressResult)
}

// FeeHistory preserves arbitrary-precision fees and reports blob-only data absent from geth's result.
func (c *Client) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethereum.FeeHistory, error) {
	return execute(c, ctx, "FeeHistory", ConversionReport{}, nil, func() (*ethapi.FeeHistoryResult, ethguard.CallResult, error) {
		return c.core.FeeHistory(ctx, blockCount, lastBlock, rewardPercentiles)
	}, feeHistoryResult)
}

// SimulateV1 keeps execution and adjudication in core; extra simulation block
// fields absent from ethclient.SimulateBlockResult are reported to the observer.
func (c *Client) SimulateV1(ctx context.Context, opts ethclient.SimulateOptions, ref *gethrpc.BlockNumberOrHash) ([]ethclient.SimulateBlockResult, error) {
	input, report, err := fromSimulate(opts)
	var base *ethapi.SimulateBlockReference
	if err == nil && ref != nil {
		v, refErr := fromBlockReference(*ref)
		err = refErr
		base = &v
	}
	return execute(c, ctx, "SimulateV1", report, err, func() ([]ethapi.SimulateBlock, ethguard.CallResult, error) {
		return c.core.SimulateV1(ctx, input, base)
	}, simulateResult)
}

// SendTransaction broadcasts the original signed transaction through core.
func (c *Client) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	var preErr error
	if tx == nil {
		preErr = conversionError("Transaction", "value", "nil transaction", nil)
	}
	_, err := execute(c, ctx, "SendTransaction", ConversionReport{}, preErr, func() (struct{}, ethguard.CallResult, error) {
		result, err := c.core.SendTransaction(ctx, tx)
		return struct{}{}, result, err
	}, identity[struct{}])
	return err
}

// SendTransactionSync broadcasts through core and converts its observed receipt.
func (c *Client) SendTransactionSync(ctx context.Context, tx *types.Transaction, timeout *time.Duration) (*types.Receipt, error) {
	var preErr error
	if tx == nil {
		preErr = conversionError("Transaction", "value", "nil transaction", nil)
	}
	return execute(c, ctx, "SendTransactionSync", ConversionReport{}, preErr, func() (*ethapi.Receipt, ethguard.CallResult, error) {
		return c.core.SendTransactionSync(ctx, tx, timeout)
	}, receiptResult)
}

// SendRawTransactionSync preserves core's broadcast and receipt-wait decisions.
func (c *Client) SendRawTransactionSync(ctx context.Context, rawTx []byte, timeout *time.Duration) (*types.Receipt, error) {
	return execute(c, ctx, "SendRawTransactionSync", ConversionReport{}, nil, func() (*ethapi.Receipt, ethguard.CallResult, error) {
		return c.core.SendRawTransactionSync(ctx, rawTx, timeout)
	}, receiptResult)
}
