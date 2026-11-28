package ethadapter

import (
	"encoding/json"
	"math/big"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// FromGethCallMsg preserves optional amounts and uses input for call data.
// Gas zero follows geth's omission convention and is reported explicitly.
func FromGethCallMsg(msg ethereum.CallMsg) (ethapi.CallMsg, ConversionReport, error) {
	report := ConversionReport{}
	fields := map[string]any{"from": msg.From, "to": msg.To}
	for _, item := range []struct {
		field string
		value *big.Int
	}{{"gasPrice", msg.GasPrice}, {"maxFeePerGas", msg.GasFeeCap}, {"maxPriorityFeePerGas", msg.GasTipCap}, {"maxFeePerBlobGas", msg.BlobGasFeeCap}, {"value", msg.Value}} {
		if item.value == nil {
			continue
		}
		if item.value.Sign() < 0 {
			return ethapi.CallMsg{}, report, conversionError("CallMsg", item.field, "negative unsigned value", nil)
		}
		fields[item.field] = "0x" + item.value.Text(16)
	}
	if msg.Gas != 0 {
		fields["gas"] = quantity(msg.Gas)
	} else {
		report.add("gas", "Go zero is omitted; explicit zero and unset cannot be distinguished")
	}
	if len(msg.Data) != 0 {
		fields["input"] = data(msg.Data)
	} else if msg.Data != nil {
		report.add("input", "empty data follows geth's omission convention")
	}
	if msg.AccessList != nil {
		fields["accessList"] = msg.AccessList
	}
	if msg.BlobHashes != nil {
		fields["blobVersionedHashes"] = msg.BlobHashes
	}
	if msg.AuthorizationList != nil {
		fields["authorizationList"] = msg.AuthorizationList
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return ethapi.CallMsg{}, report, conversionError("CallMsg", "value", "encode input", err)
	}
	out, err := ethapi.NewCallMsg(raw)
	if err != nil {
		return ethapi.CallMsg{}, report, conversionError("CallMsg", "value", "invalid input", err)
	}
	return out, report, nil
}

func blockNumberArg(n *big.Int, nilValue string) (string, error) {
	if n == nil {
		return nilValue, nil
	}
	if n.Sign() >= 0 {
		return "0x" + n.Text(16), nil
	}
	if n.IsInt64() {
		switch gethrpc.BlockNumber(n.Int64()) {
		case gethrpc.PendingBlockNumber:
			return "pending", nil
		case gethrpc.LatestBlockNumber:
			return "latest", nil
		case gethrpc.FinalizedBlockNumber:
			return "finalized", nil
		case gethrpc.SafeBlockNumber:
			return "safe", nil
		case gethrpc.EarliestBlockNumber:
			return "0x0", nil
		}
	}
	return "", conversionError("BlockReference", "number", "unknown negative block tag", nil)
}

// FromGethFilterQuery preserves hash/range exclusion and geth's default range
// (genesis through latest). Empty topic alternatives retain wildcard meaning.
func FromGethFilterQuery(q ethereum.FilterQuery) (ethapi.FilterQuery, ConversionReport, error) {
	if q.BlockHash != nil && (q.FromBlock != nil || q.ToBlock != nil) {
		return ethapi.FilterQuery{}, ConversionReport{}, conversionError("FilterQuery", "blockHash", "cannot combine hash and range", nil)
	}
	fields := map[string]any{}
	if len(q.Addresses) > 0 {
		fields["address"] = q.Addresses
	}
	if q.Topics != nil {
		fields["topics"] = q.Topics
	}
	if q.BlockHash != nil {
		fields["blockHash"] = *q.BlockHash
	} else {
		from, err := blockNumberArg(q.FromBlock, "0x0")
		if err != nil {
			return ethapi.FilterQuery{}, ConversionReport{}, err
		}
		to, err := blockNumberArg(q.ToBlock, "latest")
		if err != nil {
			return ethapi.FilterQuery{}, ConversionReport{}, err
		}
		fields["fromBlock"], fields["toBlock"] = from, to
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return ethapi.FilterQuery{}, ConversionReport{}, conversionError("FilterQuery", "value", "encode input", err)
	}
	out, err := ethapi.NewFilterQuery(raw)
	if err != nil {
		return ethapi.FilterQuery{}, ConversionReport{}, conversionError("FilterQuery", "value", "invalid input", err)
	}
	return out, ConversionReport{}, nil
}

func fromBlockReference(ref gethrpc.BlockNumberOrHash) (ethapi.BlockReference, error) {
	if (ref.BlockNumber == nil) == (ref.BlockHash == nil) {
		return ethapi.BlockReference{}, conversionError("BlockReference", "value", "exactly one of number or hash is required", nil)
	}
	if ref.BlockNumber != nil && ref.RequireCanonical {
		return ethapi.BlockReference{}, conversionError("BlockReference", "requireCanonical", "canonicality requires a hash", nil)
	}
	var value any
	if ref.BlockHash != nil {
		value = map[string]any{"blockHash": *ref.BlockHash, "requireCanonical": ref.RequireCanonical}
	} else {
		n, err := blockNumberArg(big.NewInt(int64(*ref.BlockNumber)), "latest")
		if err != nil {
			return ethapi.BlockReference{}, err
		}
		value = n
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ethapi.BlockReference{}, conversionError("BlockReference", "value", "encode reference", err)
	}
	out, err := ethapi.NewBlockReference(raw)
	if err != nil {
		return ethapi.BlockReference{}, conversionError("BlockReference", "value", "invalid reference", err)
	}
	return out, nil
}

func fromTransaction(tx *types.Transaction) (*ethapi.Transaction, ConversionReport, error) {
	if tx == nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "nil transaction", nil)
	}
	raw, err := tx.MarshalJSON()
	if err != nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "encode signed transaction", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "decode signed transaction", err)
	}
	// The native TransactionSender consumes only the signed payload of its
	// input, then reads RPC sender context itself. No recovered sender is
	// asserted here; zero is an explicit placeholder for that required DTO field.
	fields["from"] = json.RawMessage(`"0x0000000000000000000000000000000000000000"`)
	for _, field := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		fields[field] = json.RawMessage("null")
	}
	if tx.Type() != types.LegacyTxType && string(fields["accessList"]) == "null" {
		fields["accessList"] = json.RawMessage("[]")
	}
	// GasPrice is required context in the official 1559 RPC model. Sender
	// lookup binds only the signed payload, so use the transaction fee cap
	// rather than pretending this is an observed mined effective gas price.
	fields["gasPrice"], err = json.Marshal("0x" + tx.GasPrice().Text(16))
	if err != nil {
		return nil, ConversionReport{}, conversionError("Transaction", "gasPrice", "encode sender input", err)
	}
	// Sidecars are network-envelope data, absent from the signed RPC DTO.
	report := ConversionReport{}
	if tx.BlobTxSidecar() != nil {
		report.add("blobSidecar", "sender lookup consumes signed payload only; sidecar remains on caller transaction")
	}
	delete(fields, "blobs")
	delete(fields, "commitments")
	delete(fields, "proofs")
	raw, err = json.Marshal(fields)
	if err != nil {
		return nil, report, conversionError("Transaction", "value", "encode public transaction", err)
	}
	out, err := ethapi.NewTransaction(raw)
	if err != nil {
		return nil, report, conversionError("Transaction", "value", "invalid signed transaction", err)
	}
	if _, _, err := ToGethTransaction(&out); err != nil {
		return nil, report, err
	}
	return &out, report, nil
}

func fromSimulate(opts ethclient.SimulateOptions) (ethapi.SimulateV1Request, ConversionReport, error) {
	report := ConversionReport{}
	out := ethapi.SimulateV1Request{BlockStateCalls: make([]ethapi.BlockStateCall, len(opts.BlockStateCalls)), TraceTransfers: &opts.TraceTransfers, Validation: &opts.Validation, ReturnFullTransactions: &opts.ReturnFullTransactions}
	for i, block := range opts.BlockStateCalls {
		target := ethapi.BlockStateCall{}
		calls := make([]ethapi.SimulateCall, len(block.Calls))
		for j, msg := range block.Calls {
			call, itemReport, err := FromGethCallMsg(msg)
			report.merge(itemReport)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, err
			}
			// Simulate's official schema represents contract creation by omitting to,
			// while ordinary CallMsg represents it by explicit null.
			if msg.To == nil {
				call.To = nil
			}
			raw, err := json.Marshal(call)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "calls", "encode call", err)
			}
			calls[j], err = ethapi.NewSimulateCall(raw)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "calls", "invalid call", err)
			}
		}
		target.Calls = &calls
		if b := block.BlockOverrides; b != nil {
			for _, item := range []struct {
				field string
				value *big.Int
			}{{"number", b.Number}, {"difficulty", b.Difficulty}, {"baseFeePerGas", b.BaseFee}} {
				if item.value != nil && item.value.Sign() < 0 {
					return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "blockOverrides."+item.field, "negative unsigned value", nil)
				}
			}
			raw, err := json.Marshal(b)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "blockOverrides", "encode override", err)
			}
			converted, err := ethapi.NewBlockOverrides(raw)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "blockOverrides", "invalid override", err)
			}
			target.BlockOverrides = &converted
		}
		if block.StateOverrides != nil {
			for _, account := range block.StateOverrides {
				if account.Balance != nil && account.Balance.Sign() < 0 {
					return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "stateOverrides.balance", "negative unsigned value", nil)
				}
			}
			raw, err := json.Marshal(block.StateOverrides)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "stateOverrides", "encode override", err)
			}
			converted, err := ethapi.NewStateOverrides(raw)
			if err != nil {
				return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "stateOverrides", "invalid override", err)
			}
			target.StateOverrides = &converted
		}
		out.BlockStateCalls[i] = target
	}
	// Validate collection constraints without a second, hand-written schema.
	raw, err := json.Marshal(out)
	if err != nil {
		return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "value", "encode options", err)
	}
	if _, err := ethapi.NewSimulateV1Request(raw); err != nil {
		return ethapi.SimulateV1Request{}, report, conversionError("SimulateOptions", "value", "invalid options", err)
	}
	return out, report, nil
}
