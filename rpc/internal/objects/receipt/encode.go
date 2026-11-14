package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc/internal/objects/log"
)

// EncodeBlock encodes one complete eth_getBlockReceipts response using the
// same protected receipt model as Encode. Null is an absent block; [] is an
// empty block. Null or invalid elements invalidate the whole source response.
func EncodeBlock(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if ethcodec.IsJSONNull(trimmed) {
		return json.RawMessage("null"), nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("block receipts: expected an array")
	}
	encoded := make([]json.RawMessage, len(rows))
	for i, row := range rows {
		if ethcodec.IsJSONNull(row) {
			return nil, fmt.Errorf("block receipts[%d]: null receipt", i)
		}
		var err error
		encoded[i], err = Encode(row)
		if err != nil {
			return nil, fmt.Errorf("block receipts[%d]: %w", i, err)
		}
	}
	return ethcodec.MarshalNoEscape(encoded)
}

// Encode checks one eth_getTransactionReceipt result and returns the canonical
// JSON of the official fields plus receipt integrity protections. JSON null
// stays null. Optional nullable addresses preserve absence and explicit null
// distinctly. An empty logs array stays [].
//
// Every protected field is checked here, so a missing or malformed one is a
// fault rather than a default. Each log's nonzero transactionHash and blockHash
// must match the receipt, and logsBloom must equal CreateBloom over the logs
// this function just built: the bloom that ships is the bloom that
// was checked. Binding a receipt to the request it answers is AdmitTransaction
// and AdmitBlock, not this function.
// Addresses and hashes are lowercase. Quantities are canonical QUANTITY.
// Log data keeps leading zeros. Array order is unchanged. JSON is compact,
// HTML escaping is off, and field order is the encoder struct order.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if ethcodec.IsJSONNull(trimmed) {
		return json.RawMessage("null"), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("receipt: expected an object")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(fields["logs"], &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("receipt: logs must be an array")
	}
	logs := make([]canonicalReceiptLog, len(rows))
	for i, row := range rows {
		item, err := encodeReceiptLog(row)
		if err != nil {
			return nil, fmt.Errorf("receipt logs[%d]: %w", i, err)
		}
		logs[i] = item
	}
	// The official receipt parser must see the shared encoder's log objects,
	// not a separate interpretation of the upstream logs. In particular, the
	// existing log contract treats blockTimestamp:null as omission.
	canonicalLogs, err := ethcodec.MarshalNoEscape(logs)
	if err != nil {
		return nil, err
	}
	fields["logs"] = canonicalLogs
	checked, err := ethcodec.MarshalNoEscape(fields)
	if err != nil {
		return nil, err
	}
	out, err := ethapi.NewReceipt(checked)
	if err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	if out.Status == nil && out.Root == nil {
		return nil, fmt.Errorf("receipt: missing status or root")
	}
	// The official field description defines an execution outcome, even
	// though its machine pattern accepts a wider QUANTITY domain.
	if out.Status != nil && *out.Status != "0x0" && *out.Status != "0x1" {
		return nil, fmt.Errorf("receipt: status must be failure (0) or success (1)")
	}
	if err := checkLogBindings(logs, string(out.TransactionHash), string(out.BlockHash)); err != nil {
		return nil, err
	}
	if string(out.LogsBloom) != canonicalBloom(logs) {
		return nil, fmt.Errorf("receipt: logsBloom disagrees with logs")
	}
	return ethcodec.MarshalNoEscape(out)
}

// zeroHash is the spelling a source writes when it leaves a hash out. The
// admission check this replaces compared a log's hashes only when they were
// non-zero, so Encode keeps that tolerance: a zero is an omission, not a
// claim about a different transaction. Without it this step would silently
// turn a Corroborated receipt into an encoding fault.
var zeroHash = common.Hash{}.Hex()

// checkLogBindings binds every log to the receipt that carries it. Both sides
// are already canonical, so a mismatch is a source disagreeing with itself
// rather than two spellings of one value.
func checkLogBindings(logs []canonicalReceiptLog, transactionHash, blockHash string) error {
	for i, item := range logs {
		if item.TransactionHash != zeroHash && item.TransactionHash != transactionHash {
			return fmt.Errorf("receipt logs[%d]: transactionHash %s does not match receipt transactionHash %s", i, item.TransactionHash, transactionHash)
		}
		if item.BlockHash != zeroHash && item.BlockHash != blockHash {
			return fmt.Errorf("receipt logs[%d]: blockHash %s does not match receipt blockHash %s", i, item.BlockHash, blockHash)
		}
	}
	return nil
}

// canonicalBloom builds the bloom of the logs this encoder just produced, so
// the emitted logsBloom is checked against the value EthGuard ships rather
// than against a second decoding of the source payload.
func canonicalBloom(logs []canonicalReceiptLog) string {
	decoded := make([]*types.Log, len(logs))
	for i, item := range logs {
		entry := &types.Log{Address: common.HexToAddress(item.Address)}
		entry.Topics = make([]common.Hash, len(item.Topics))
		for j, topic := range item.Topics {
			entry.Topics[j] = common.HexToHash(topic)
		}
		decoded[i] = entry
	}
	return hexutil.Encode(types.CreateBloom(&types.Receipt{Logs: decoded}).Bytes())
}

// encodeReceiptLog canonicalizes one log embedded in a receipt through the
// shared log encoder: the receipt-side requiredness (missing topics, missing
// removed, field shapes) is exactly log.Validate's rule set, with identical
// messages, so both call sites of the log object accept and reject the same
// payloads.
func encodeReceiptLog(value json.RawMessage) (canonicalReceiptLog, error) {
	out, err := log.EncodeOne(value)
	if err != nil {
		return canonicalReceiptLog{}, err
	}
	return out, nil
}

// canonicalReceipt shares the official receipt shape, including nullable
// optional addresses and the bounded transaction type.
type canonicalReceipt = ethapi.Receipt

// canonicalReceiptLog is the shared protected log object; the receipt log's
// JSON shape is byte-identical to the eth_getLogs log, so the field set and
// its fixed order live once in the log package.
type canonicalReceiptLog = log.CanonicalLog
