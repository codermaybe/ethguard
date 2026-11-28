package ethadapter

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
)

// ToGethHeader converts all header commitments and verifies the advertised hash.
func ToGethHeader(h *ethapi.Header) (*types.Header, ConversionReport, error) {
	if h == nil {
		return nil, ConversionReport{}, conversionError("Header", "value", "nil header", nil)
	}
	c := converter{kind: "Header"}
	out := &types.Header{
		ParentHash: c.hash("parentHash", string(h.ParentHash)), UncleHash: c.hash("sha3Uncles", string(h.Sha3uncles)),
		Coinbase: c.address("miner", string(h.Miner)), Root: c.hash("stateRoot", string(h.StateRoot)),
		TxHash: c.hash("transactionsRoot", string(h.TransactionsRoot)), ReceiptHash: c.hash("receiptsRoot", string(h.ReceiptsRoot)),
		Difficulty: c.big("difficulty", string(h.Difficulty)), Number: c.big("number", string(h.Number)),
		GasLimit: c.u64("gasLimit", string(h.GasLimit)), GasUsed: c.u64("gasUsed", string(h.GasUsed)), Time: c.u64("timestamp", string(h.Timestamp)),
		Extra: c.bytes("extraData", string(h.ExtraData), -1), MixDigest: c.hash("mixHash", string(h.MixHash)),
		BaseFee: c.optionalBig("baseFeePerGas", h.BaseFeePerGas), WithdrawalsHash: c.optionalHash("withdrawalsRoot", h.WithdrawalsRoot),
		BlobGasUsed: c.optionalU64("blobGasUsed", h.BlobGasUsed), ExcessBlobGas: c.optionalU64("excessBlobGas", h.ExcessBlobGas),
		ParentBeaconRoot: c.optionalHash("parentBeaconBlockRoot", h.ParentBeaconBlockRoot), RequestsHash: c.optionalHash("requestsHash", h.RequestsHash),
		BlockAccessListHash: c.optionalHash("blockAccessListHash", h.BlockAccessListHash),
	}
	copy(out.Bloom[:], c.bytes("logsBloom", string(h.LogsBloom), len(out.Bloom)))
	copy(out.Nonce[:], c.bytes("nonce", string(h.Nonce), len(out.Nonce)))
	if h.SlotNumber != nil {
		n := c.u64("slotNumber", string(*h.SlotNumber))
		out.SlotNumber = &n
	}
	want := c.hash("hash", string(h.Hash))
	if c.err != nil {
		return nil, c.report, c.err
	}
	if out.Hash() != want {
		return nil, c.report, conversionError("Header", "hash", "header hash mismatch", nil)
	}
	return out, c.report, nil
}

// ToGethTransaction converts a signed transaction, validates its signature, and
// verifies the payload hash. The reported RPC context does not belong to a
// types.Transaction. The public ethapi transaction has no blob-sidecar carrier.
func ToGethTransaction(tx *ethapi.Transaction) (*types.Transaction, ConversionReport, error) {
	if tx == nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "nil transaction", nil)
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "invalid union", err)
	}
	if _, err := ethapi.NewTransaction(raw); err != nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "invalid public model", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, ConversionReport{}, conversionError("Transaction", "value", "invalid JSON", err)
	}
	c := converter{kind: "Transaction"}
	text := func(field string) string {
		var value string
		if err := json.Unmarshal(fields[field], &value); err != nil {
			c.fail(field, "expected quantity or hex string", err)
		}
		return value
	}
	getBig := func(field string) *big.Int { return c.big(field, text(field)) }
	getU64 := func(field string) uint64 { return c.u64(field, text(field)) }
	var to *common.Address
	if value := fields["to"]; len(value) != 0 && string(value) != "null" {
		a := c.address("to", text("to"))
		to = &a
	}
	var access types.AccessList
	if value := fields["accessList"]; len(value) != 0 {
		if err := json.Unmarshal(value, &access); err != nil {
			c.fail("accessList", "invalid access list", err)
		}
	}
	var out *types.Transaction
	typ := getU64("type")
	if typ <= types.DynamicFeeTxType {
		nonce, gas, value, input := getU64("nonce"), getU64("gas"), getBig("value"), c.bytes("input", text("input"), -1)
		r, s := getBig("r"), getBig("s")
		var v *big.Int
		if typ == types.LegacyTxType {
			v = getBig("v")
		} else {
			// yParity is a byte in the standard model, whereas V is a quantity.
			parity := c.hexByte("yParity", text("yParity"))
			v = new(big.Int).SetUint64(uint64(parity))
			if parity > 1 {
				c.fail("yParity", "must be zero or one", nil)
			}
			if len(fields["v"]) != 0 && c.hexByte("v", text("v")) != parity {
				c.fail("v", "disagrees with yParity", nil)
			}
		}
		switch typ {
		case types.LegacyTxType:
			out = types.NewTx(&types.LegacyTx{Nonce: nonce, GasPrice: getBig("gasPrice"), Gas: gas, To: to, Value: value, Data: input, V: v, R: r, S: s})
			if len(fields["chainId"]) != 0 && out.ChainId().Cmp(getBig("chainId")) != 0 {
				c.fail("chainId", "disagrees with signature V", nil)
			}
		case types.AccessListTxType:
			out = types.NewTx(&types.AccessListTx{ChainID: getBig("chainId"), Nonce: nonce, GasPrice: getBig("gasPrice"), Gas: gas, To: to, Value: value, Data: input, AccessList: access, V: v, R: r, S: s})
		case types.DynamicFeeTxType:
			out = types.NewTx(&types.DynamicFeeTx{ChainID: getBig("chainId"), Nonce: nonce, GasTipCap: getBig("maxPriorityFeePerGas"), GasFeeCap: getBig("maxFeePerGas"), Gas: gas, To: to, Value: value, Data: input, AccessList: access, V: v, R: r, S: s})
		}
	} else if typ == types.BlobTxType || typ == types.SetCodeTxType {
		// Geth's blob/set-code codec uses MustFromBig for several uint256
		// fields. Check every protocol-width quantity before invoking it so
		// hostile direct ethapi values produce ConversionError, never panic.
		for _, field := range []string{"chainId", "maxPriorityFeePerGas", "maxFeePerGas", "maxFeePerBlobGas", "value", "r", "s"} {
			if len(fields[field]) != 0 && getBig(field).BitLen() > 256 {
				c.fail(field, "overflows uint256", nil)
			}
		}
		if c.err != nil {
			return nil, c.report, c.err
		}
		// The codec owns the actual blob/auth tuple representation.
		out = new(types.Transaction)
		for _, field := range []string{"yParity", "v"} {
			if len(fields[field]) != 0 {
				fields[field], err = json.Marshal(quantity(uint64(c.hexByte(field, text(field)))))
				if err != nil {
					c.fail(field, "encode signature byte", err)
				}
			}
		}
		if typ == types.SetCodeTxType {
			var authorizations []map[string]json.RawMessage
			if err := json.Unmarshal(fields["authorizationList"], &authorizations); err != nil {
				c.fail("authorizationList", "invalid authorizations", err)
			}
			for _, auth := range authorizations {
				var parity string
				if err := json.Unmarshal(auth["yParity"], &parity); err != nil {
					c.fail("authorizationList.yParity", "invalid parity", err)
					continue
				}
				b := c.hexByte("authorizationList.yParity", parity)
				if b > 1 {
					c.fail("authorizationList.yParity", "must be zero or one", nil)
				}
				encoded, err := json.Marshal(quantity(uint64(b)))
				if err != nil {
					c.fail("authorizationList.yParity", "encode parity", err)
				}
				auth["yParity"] = encoded
			}
			encoded, err := json.Marshal(authorizations)
			if err != nil {
				c.fail("authorizationList", "encode authorizations", err)
			}
			fields["authorizationList"] = encoded
		}
		normalized, marshalErr := json.Marshal(fields)
		if marshalErr != nil {
			c.fail("value", "encode typed transaction", marshalErr)
		} else if err := json.Unmarshal(normalized, out); err != nil {
			c.fail("value", "invalid typed transaction", err)
		}
	} else {
		c.fail("type", "unsupported transaction type", nil)
	}
	want := c.hash("hash", text("hash"))
	if c.err != nil {
		return nil, c.report, c.err
	}
	v, r, sigS := out.RawSignatureValues()
	parity := new(big.Int).Set(v)
	if typ == types.LegacyTxType {
		if v.Cmp(big.NewInt(35)) >= 0 {
			parity.Sub(v, big.NewInt(35))
			parity.And(parity, big.NewInt(1))
		} else {
			parity.Sub(v, big.NewInt(27))
		}
	}
	if !parity.IsUint64() || parity.Uint64() > 1 || !crypto.ValidateSignatureValues(byte(parity.Uint64()), r, sigS, false) {
		return nil, c.report, conversionError("Transaction", "signature", "invalid signed transaction", nil)
	}
	if out.Hash() != want {
		return nil, c.report, conversionError("Transaction", "hash", "transaction hash mismatch", nil)
	}
	for _, field := range []string{"from", "blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		c.report.add(field, "RPC context is not carried by types.Transaction")
	}
	if typ == types.DynamicFeeTxType || typ == types.BlobTxType || typ == types.SetCodeTxType {
		if len(fields["gasPrice"]) != 0 {
			c.report.add("gasPrice", "effective RPC gas price is not a signed dynamic-fee transaction field")
		}
	}
	if typ == types.BlobTxType {
		c.report.add("blobSidecar", "ethapi exposes signed blob hashes but does not carry a blob sidecar")
	}
	return out, c.report, nil
}

// ToGethBlock requires complete transactions and matching uncle headers. It
// verifies body roots in addition to every advertised header/transaction hash.
func ToGethBlock(b *ethapi.Block, uncles []*ethapi.Header) (*types.Block, ConversionReport, error) {
	if b == nil {
		return nil, ConversionReport{}, conversionError("Block", "value", "nil block", nil)
	}
	c := converter{kind: "Block"}
	if b.Difficulty == nil {
		return nil, c.report, conversionError("Block", "difficulty", "missing header commitment", nil)
	}
	h, report, err := ToGethHeader(&ethapi.Header{
		BaseFeePerGas: b.BaseFeePerGas, BlobGasUsed: b.BlobGasUsed, BlockAccessListHash: b.BlockAccessListHash, Difficulty: *b.Difficulty,
		ExcessBlobGas: b.ExcessBlobGas, ExtraData: b.ExtraData, GasLimit: b.GasLimit, GasUsed: b.GasUsed, Hash: b.Hash, LogsBloom: b.LogsBloom,
		Miner: b.Miner, MixHash: b.MixHash, Nonce: b.Nonce, Number: b.Number, ParentBeaconBlockRoot: b.ParentBeaconBlockRoot, ParentHash: b.ParentHash,
		ReceiptsRoot: b.ReceiptsRoot, RequestsHash: b.RequestsHash, Sha3uncles: b.Sha3uncles, SlotNumber: b.SlotNumber, StateRoot: b.StateRoot,
		Timestamp: b.Timestamp, TransactionsRoot: b.TransactionsRoot, WithdrawalsRoot: b.WithdrawalsRoot,
	})
	if err != nil {
		return nil, report, err
	}
	c.report.merge(report)
	c.big("size", string(b.Size))
	c.report.add("size", "provider-reported size is not retained; types.Block computes its encoded size")
	if len(uncles) != len(b.Uncles) {
		return nil, c.report, conversionError("Block", "uncles", "complete matching uncle headers are required", nil)
	}
	body := types.Body{Uncles: make([]*types.Header, len(uncles))}
	for i, uncle := range uncles {
		converted, extra, err := ToGethHeader(uncle)
		c.report.merge(extra)
		if err != nil {
			return nil, c.report, err
		}
		if converted.Hash() != c.hash(fmt.Sprintf("uncles[%d]", i), string(b.Uncles[i])) {
			c.fail("uncles", "uncle hash mismatch", nil)
		}
		body.Uncles[i] = converted
	}
	if types.CalcUncleHash(body.Uncles) != h.UncleHash {
		c.fail("sha3Uncles", "uncle root mismatch", nil)
	}
	if (b.Transactions.FullTransactions == nil) == (b.Transactions.TransactionHashes == nil) {
		c.fail("transactions", "exactly one transaction representation is required", nil)
	}
	if b.Transactions.TransactionHashes != nil && len(*b.Transactions.TransactionHashes) != 0 {
		c.fail("transactions", "full transaction bodies are required", nil)
	}
	if b.Transactions.FullTransactions != nil {
		body.Transactions = make([]*types.Transaction, len(*b.Transactions.FullTransactions))
		for i, item := range *b.Transactions.FullTransactions {
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, c.report, conversionError("Block", "transactions", "invalid transaction union", err)
			}
			tx, err := ethapi.NewTransaction(raw)
			if err != nil {
				return nil, c.report, conversionError("Block", "transactions", "invalid transaction", err)
			}
			converted, extra, err := ToGethTransaction(&tx)
			c.report.merge(extra)
			if err != nil {
				return nil, c.report, err
			}
			body.Transactions[i] = converted
		}
	}
	if c.err != nil {
		return nil, c.report, c.err
	}
	if types.DeriveSha(types.Transactions(body.Transactions), trie.NewStackTrie(nil)) != h.TxHash {
		return nil, c.report, conversionError("Block", "transactionsRoot", "transaction trie root mismatch", nil)
	}
	if (b.Withdrawals == nil) != (h.WithdrawalsHash == nil) {
		return nil, c.report, conversionError("Block", "withdrawals", "withdrawals presence disagrees with header", nil)
	}
	if b.Withdrawals != nil {
		body.Withdrawals = make([]*types.Withdrawal, len(*b.Withdrawals))
		for i, w := range *b.Withdrawals {
			body.Withdrawals[i] = &types.Withdrawal{Index: c.u64("withdrawals.index", string(w.Index)), Validator: c.u64("withdrawals.validatorIndex", string(w.ValidatorIndex)), Address: c.address("withdrawals.address", string(w.Address)), Amount: c.u64("withdrawals.amount", string(w.Amount))}
		}
		if types.DeriveSha(types.Withdrawals(body.Withdrawals), trie.NewStackTrie(nil)) != *h.WithdrawalsHash {
			c.fail("withdrawalsRoot", "withdrawal trie root mismatch", nil)
		}
	}
	if c.err != nil {
		return nil, c.report, c.err
	}
	return types.NewBlockWithHeader(h).WithBody(body), c.report, nil
}

func blockResult(b *ethapi.Block, result ethguard.CallResult) (*types.Block, ConversionReport, error) {
	var uncles []*ethapi.Header
	if result.Decision != nil && len(result.Decision.Provenance) != 0 {
		var raws []json.RawMessage
		if err := json.Unmarshal(result.Decision.Provenance, &raws); err != nil {
			return nil, ConversionReport{}, conversionError("Block", "provenance", "expected admitted uncle header array", err)
		}
		uncles = make([]*ethapi.Header, len(raws))
		for i, raw := range raws {
			// The provenance retains the source's block-shaped uncle response.
			// Decode only Header's public field set, then revalidate commitments.
			var header ethapi.Header
			if err := json.Unmarshal(raw, &header); err != nil {
				return nil, ConversionReport{}, conversionError("Block", "provenance", "invalid uncle header", err)
			}
			uncles[i] = &header
		}
	}
	return ToGethBlock(b, uncles)
}

// ToGethLog preserves known log fields and reports absent scalar fields whose
// presence is not represented by types.Log.
func ToGethLog(l ethapi.Log) (types.Log, ConversionReport, error) {
	c := converter{kind: "Log"}
	out := types.Log{TxHash: c.hash("transactionHash", string(l.TransactionHash))}
	if l.Address != nil {
		out.Address = c.address("address", string(*l.Address))
	} else {
		c.report.add("address", "absent becomes Go zero value")
	}
	if l.Topics != nil {
		out.Topics = make([]common.Hash, len(*l.Topics))
		for i, topic := range *l.Topics {
			out.Topics[i] = c.hash("topics", string(topic))
		}
	}
	if l.Data != nil {
		out.Data = c.bytes("data", string(*l.Data), -1)
	}
	if l.BlockHash != nil {
		out.BlockHash = c.hash("blockHash", string(*l.BlockHash))
	} else {
		c.report.add("blockHash", "absent becomes Go zero value")
	}
	if l.BlockNumber != nil {
		out.BlockNumber = c.u64("blockNumber", string(*l.BlockNumber))
	} else {
		c.report.add("blockNumber", "absent becomes Go zero value")
	}
	if l.TransactionIndex != nil {
		out.TxIndex = c.uint("transactionIndex", string(*l.TransactionIndex))
	} else {
		c.report.add("transactionIndex", "absent becomes Go zero value")
	}
	if l.LogIndex != nil {
		out.Index = c.uint("logIndex", string(*l.LogIndex))
	} else {
		c.report.add("logIndex", "absent becomes Go zero value")
	}
	if l.Removed != nil {
		out.Removed = *l.Removed
	} else {
		c.report.add("removed", "absent becomes Go false")
	}
	if l.BlockTimestamp != nil {
		out.BlockTimestamp = c.u64("blockTimestamp", string(*l.BlockTimestamp))
	} else {
		c.report.add("blockTimestamp", "absent becomes Go zero value")
	}
	if c.err != nil {
		return types.Log{}, c.report, c.err
	}
	return out, c.report, nil
}

// ToGethReceipt preserves receipt fields and reports RPC sender/destination and
// optional scalar presence distinctions absent from types.Receipt.
func ToGethReceipt(r *ethapi.Receipt) (*types.Receipt, ConversionReport, error) {
	if r == nil {
		return nil, ConversionReport{}, conversionError("Receipt", "value", "nil receipt", nil)
	}
	c := converter{kind: "Receipt"}
	out := &types.Receipt{TxHash: c.hash("transactionHash", string(r.TransactionHash)), TransactionIndex: c.uint("transactionIndex", string(r.TransactionIndex)), BlockHash: c.hash("blockHash", string(r.BlockHash)), BlockNumber: c.big("blockNumber", string(r.BlockNumber)), CumulativeGasUsed: c.u64("cumulativeGasUsed", string(r.CumulativeGasUsed)), GasUsed: c.u64("gasUsed", string(r.GasUsed)), EffectiveGasPrice: c.big("effectiveGasPrice", string(r.EffectiveGasPrice))}
	copy(out.Bloom[:], c.bytes("logsBloom", string(r.LogsBloom), len(out.Bloom)))
	c.address("from", string(r.From))
	c.report.add("from", "RPC sender is not carried by types.Receipt")
	if r.To != nil && *r.To != nil {
		c.address("to", string(**r.To))
	}
	c.report.add("to", "RPC destination is not carried by types.Receipt")
	if r.ContractAddress != nil && *r.ContractAddress != nil {
		out.ContractAddress = c.address("contractAddress", string(**r.ContractAddress))
	} else {
		c.report.add("contractAddress", "absent or null becomes Go zero address")
	}
	if r.Type != nil {
		out.Type = c.hexByte("type", string(*r.Type))
		if out.Type > types.SetCodeTxType {
			c.fail("type", "unsupported receipt type", nil)
		}
	} else {
		c.report.add("type", "absent becomes legacy type zero")
	}
	if r.Status != nil {
		out.Status = c.u64("status", string(*r.Status))
		if out.Status > 1 {
			c.fail("status", "must be zero or one", nil)
		}
	} else {
		c.report.add("status", "absent becomes Go zero value")
	}
	if r.Root != nil {
		out.PostState = c.bytes("root", string(*r.Root), common.HashLength)
	}
	if r.Status == nil && r.Root == nil {
		c.fail("status", "status or post-state root is required", nil)
	}
	if r.BlobGasUsed != nil {
		out.BlobGasUsed = c.u64("blobGasUsed", string(*r.BlobGasUsed))
	} else {
		c.report.add("blobGasUsed", "absent becomes Go zero value")
	}
	out.BlobGasPrice = c.optionalBig("blobGasPrice", r.BlobGasPrice)
	out.Logs = make([]*types.Log, len(r.Logs))
	for i, log := range r.Logs {
		converted, report, err := ToGethLog(log)
		c.report.merge(report)
		if err != nil {
			return nil, c.report, err
		}
		out.Logs[i] = &converted
	}
	if c.err != nil {
		return nil, c.report, c.err
	}
	return out, c.report, nil
}
