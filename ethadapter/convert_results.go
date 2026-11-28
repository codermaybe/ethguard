package ethadapter

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func syncProgressResult(v *ethapi.SyncProgress, _ ethguard.CallResult) (*ethereum.SyncProgress, ConversionReport, error) {
	if v == nil {
		return nil, ConversionReport{}, nil
	}
	c := converter{kind: "SyncProgress"}
	out := &ethereum.SyncProgress{}
	for _, field := range []struct {
		name string
		src  *ethapi.Quantity
		dst  *uint64
	}{{"startingBlock", v.StartingBlock, &out.StartingBlock}, {"currentBlock", v.CurrentBlock, &out.CurrentBlock}, {"highestBlock", v.HighestBlock, &out.HighestBlock}} {
		if field.src == nil {
			c.report.add(field.name, "absent standard counter becomes Go zero value")
		} else {
			*field.dst = c.u64(field.name, string(*field.src))
		}
	}
	c.report.add("gethSyncCounters", "geth-specific state/indexing counters are outside the standard ethapi model; zero does not prove completion")
	if c.err != nil {
		return nil, c.report, c.err
	}
	return out, c.report, nil
}

func feeHistoryResult(v *ethapi.FeeHistoryResult, _ ethguard.CallResult) (*ethereum.FeeHistory, ConversionReport, error) {
	if v == nil {
		return nil, ConversionReport{}, conversionError("FeeHistory", "value", "nil result", nil)
	}
	c := converter{kind: "FeeHistory"}
	out := &ethereum.FeeHistory{OldestBlock: c.big("oldestBlock", string(v.OldestBlock)), BaseFee: make([]*big.Int, len(v.BaseFeePerGas)), GasUsedRatio: make([]float64, len(v.GasUsedRatio))}
	for i, n := range v.BaseFeePerGas {
		out.BaseFee[i] = c.big("baseFeePerGas", string(n))
	}
	if len(v.GasUsedRatio) != 0 {
		c.report.add("gasUsedRatio", "exact decimal ratios are represented as float64, with IEEE-754 rounding")
	}
	for i, n := range v.GasUsedRatio {
		value, err := strconv.ParseFloat(string(n), 64)
		out.GasUsedRatio[i] = value
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			c.fail("gasUsedRatio", "non-finite ratio", nil)
		}
	}
	if v.Reward != nil {
		out.Reward = make([][]*big.Int, len(*v.Reward))
		for i, row := range *v.Reward {
			out.Reward[i] = make([]*big.Int, len(row))
			for j, n := range row {
				out.Reward[i][j] = c.big("reward", string(n))
			}
		}
	}
	if v.BaseFeePerBlobGas != nil {
		for _, n := range *v.BaseFeePerBlobGas {
			c.big("baseFeePerBlobGas", string(n))
		}
		c.report.add("baseFeePerBlobGas", "ethereum.FeeHistory has no blob fee field")
	}
	if v.BlobGasUsedRatio != nil {
		for _, n := range *v.BlobGasUsedRatio {
			value, err := strconv.ParseFloat(string(n), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				c.fail("blobGasUsedRatio", "non-finite ratio", err)
			}
		}
		c.report.add("blobGasUsedRatio", "ethereum.FeeHistory has no blob ratio field")
	}
	if c.err != nil {
		return nil, c.report, c.err
	}
	return out, c.report, nil
}

func simulateResult(rows []ethapi.SimulateBlock, _ ethguard.CallResult) ([]ethclient.SimulateBlockResult, ConversionReport, error) {
	c := converter{kind: "SimulateBlock"}
	out := make([]ethclient.SimulateBlockResult, len(rows))
	for i, b := range rows {
		// Validate the entire public model before selecting the smaller geth
		// result, so omitted malformed fields are not quietly accepted.
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, c.report, conversionError(c.kind, "value", "invalid simulation block", err)
		}
		if _, err := ethapi.NewSimulateBlock(raw); err != nil {
			return nil, c.report, conversionError(c.kind, "value", "invalid simulation block", err)
		}
		v := ethclient.SimulateBlockResult{Number: c.big("number", string(b.Number)), Hash: c.hash("hash", string(b.Hash)), Timestamp: c.u64("timestamp", string(b.Timestamp)), GasLimit: c.u64("gasLimit", string(b.GasLimit)), GasUsed: c.u64("gasUsed", string(b.GasUsed)), FeeRecipient: c.address("miner", string(b.Miner)), BaseFeePerGas: c.optionalBig("baseFeePerGas", b.BaseFeePerGas), Calls: make([]ethclient.SimulateCallResult, len(b.Calls))}
		for j, call := range b.Calls {
			var result ethclient.SimulateCallResult
			var max *ethapi.Quantity64
			if success := call.ResultOfCallSuccess; success != nil {
				result.GasUsed = c.u64("calls.gasUsed", string(success.GasUsed))
				result.ReturnValue = c.bytes("calls.returnData", string(success.ReturnData), -1)
				result.Status = c.u64("calls.status", string(success.Status))
				max = success.MaxUsedGas
				result.Logs = make([]*types.Log, len(success.Logs))
				for k, l := range success.Logs {
					log, report, err := ToGethLog(l)
					c.report.merge(report)
					if err != nil {
						return nil, c.report, err
					}
					result.Logs[k] = &log
				}
			} else if failure := call.ResultOfCallFailure; failure != nil {
				c.report.add("calls.error.data", "the standard simulation error model carries code/message; provider data visibility belongs to CallResult evidence")
				result.GasUsed = c.u64("calls.gasUsed", string(failure.GasUsed))
				result.ReturnValue = c.bytes("calls.returnData", string(failure.ReturnData), -1)
				result.Status = c.u64("calls.status", string(failure.Status))
				max = failure.MaxUsedGas
				if e := failure.Error.SimulateErrorRevert; e != nil {
					result.Error = &ethclient.CallError{Code: e.Code, Message: string(e.Message)}
				} else if e := failure.Error.SimulateErrorVM; e != nil {
					result.Error = &ethclient.CallError{Code: e.Code, Message: string(e.Message)}
				}
			}
			if max != nil {
				result.MaxUsedGas = c.u64("calls.maxUsedGas", string(*max))
			} else {
				c.report.add("calls.maxUsedGas", "absent becomes Go zero value")
			}
			v.Calls[j] = result
		}
		out[i] = v
	}
	if len(rows) != 0 {
		c.report.add("simulationBlockFields", "ethclient.SimulateBlockResult omits transactions, uncles, withdrawals and header fields outside its result surface")
	}
	if c.err != nil {
		return nil, c.report, c.err
	}
	return out, c.report, nil
}
