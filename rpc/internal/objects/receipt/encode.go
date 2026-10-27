package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/rpc/internal/objects/log"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// EncodeBlock encodes one complete eth_getBlockReceipts response using the
// same protected receipt model as Encode. Null is an absent block; [] is an
// empty block. Null or invalid elements invalidate the whole source response.
func EncodeBlock(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if wire.IsJSONNull(trimmed) {
		return json.RawMessage("null"), nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("block receipts: expected an array")
	}
	encoded := make([]json.RawMessage, len(rows))
	for i, row := range rows {
		if wire.IsJSONNull(row) {
			return nil, fmt.Errorf("block receipts[%d]: null receipt", i)
		}
		var err error
		encoded[i], err = Encode(row)
		if err != nil {
			return nil, fmt.Errorf("block receipts[%d]: %w", i, err)
		}
	}
	return wire.MarshalNoEscape(encoded)
}

// Encode checks one eth_getTransactionReceipt result and returns the canonical
// JSON of the protected fields. JSON null stays null. contractAddress, to,
// blobGasUsed, and blobGasPrice are omitted when absent. A JSON null
// contractAddress is omitted. An empty logs array stays [].
//
// Every protected field is checked here, so a missing or malformed one is a
// fault rather than a default. Each log must carry the receipt's own
// transactionHash and blockHash, and logsBloom must equal CreateBloom over
// the logs this function just built: the bloom that ships is the bloom that
// was checked. Binding a receipt to the request it answers is AdmitTransaction
// and AdmitBlock, not this function.
// Addresses and hashes are lowercase. Quantities are canonical QUANTITY.
// Log data keeps leading zeros. Array order is unchanged. JSON is compact,
// HTML escaping is off, and field order is the encoder struct order.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if wire.IsJSONNull(trimmed) {
		return json.RawMessage("null"), nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("receipt: expected an object")
	}
	var raw struct {
		TransactionHash   json.RawMessage   `json:"transactionHash"`
		TransactionIndex  json.RawMessage   `json:"transactionIndex"`
		BlockHash         json.RawMessage   `json:"blockHash"`
		BlockNumber       json.RawMessage   `json:"blockNumber"`
		From              json.RawMessage   `json:"from"`
		CumulativeGasUsed json.RawMessage   `json:"cumulativeGasUsed"`
		GasUsed           json.RawMessage   `json:"gasUsed"`
		EffectiveGasPrice json.RawMessage   `json:"effectiveGasPrice"`
		LogsBloom         json.RawMessage   `json:"logsBloom"`
		Logs              []json.RawMessage `json:"logs"`
		Status            json.RawMessage   `json:"status"`
		Root              json.RawMessage   `json:"root"`
		Type              json.RawMessage   `json:"type"`
		ContractAddress   json.RawMessage   `json:"contractAddress"`
		To                json.RawMessage   `json:"to"`
		BlobGasUsed       json.RawMessage   `json:"blobGasUsed"`
		BlobGasPrice      json.RawMessage   `json:"blobGasPrice"`
	}
	if err := json.Unmarshal(trimmed, &raw); err != nil || raw.Logs == nil {
		return nil, fmt.Errorf("receipt: malformed response")
	}
	logs := make([]canonicalReceiptLog, len(raw.Logs))
	for i, row := range raw.Logs {
		item, err := encodeReceiptLog(row)
		if err != nil {
			return nil, fmt.Errorf("receipt logs[%d]: %w", i, err)
		}
		logs[i] = item
	}
	out := canonicalReceipt{Logs: logs}
	var err error
	if out.TransactionHash, err = wire.ValidateWireFixedHex(raw.TransactionHash, 64, "hash"); err != nil {
		return nil, fmt.Errorf("receipt transactionHash: %w", err)
	}
	if out.TransactionIndex, err = wire.CanonicalQuantity(raw.TransactionIndex); err != nil {
		return nil, fmt.Errorf("receipt transactionIndex: %w", err)
	}
	if out.BlockHash, err = wire.ValidateWireFixedHex(raw.BlockHash, 64, "hash"); err != nil {
		return nil, fmt.Errorf("receipt blockHash: %w", err)
	}
	if out.BlockNumber, err = wire.CanonicalQuantity(raw.BlockNumber); err != nil {
		return nil, fmt.Errorf("receipt blockNumber: %w", err)
	}
	if out.From, err = optionalAddress(raw.From); err != nil {
		return nil, fmt.Errorf("receipt from: %w", err)
	}
	if out.CumulativeGasUsed, err = wire.CanonicalQuantity(raw.CumulativeGasUsed); err != nil {
		return nil, fmt.Errorf("receipt cumulativeGasUsed: %w", err)
	}
	if out.GasUsed, err = wire.CanonicalQuantity(raw.GasUsed); err != nil {
		return nil, fmt.Errorf("receipt gasUsed: %w", err)
	}
	if out.EffectiveGasPrice, err = optionalQuantity(raw.EffectiveGasPrice); err != nil {
		return nil, fmt.Errorf("receipt effectiveGasPrice: %w", err)
	}
	if out.LogsBloom, err = wire.ValidateWireFixedHex(raw.LogsBloom, 512, "logsBloom"); err != nil {
		return nil, fmt.Errorf("receipt logsBloom: %w", err)
	}
	if err := checkLogBindings(logs, out.TransactionHash, out.BlockHash); err != nil {
		return nil, err
	}
	if out.LogsBloom != canonicalBloom(logs) {
		return nil, fmt.Errorf("receipt: logsBloom disagrees with logs")
	}
	if wire.PresentNonNull(raw.Status) {
		status, err := wire.CanonicalQuantity(raw.Status)
		if err != nil {
			return nil, fmt.Errorf("receipt status: %w", err)
		}
		out.Status = &status
	} else {
		root, err := wire.ValidateWireFixedHex(raw.Root, 64, "root")
		if err != nil {
			return nil, fmt.Errorf("receipt root: %w", err)
		}
		out.Root = &root
	}
	if out.Type, err = optionalQuantity(raw.Type); err != nil {
		return nil, fmt.Errorf("receipt type: %w", err)
	}
	if out.ContractAddress, err = optionalAddress(raw.ContractAddress); err != nil {
		return nil, fmt.Errorf("receipt contractAddress: %w", err)
	}
	if out.To, err = optionalAddress(raw.To); err != nil {
		return nil, fmt.Errorf("receipt to: %w", err)
	}
	if out.BlobGasUsed, err = optionalQuantity(raw.BlobGasUsed); err != nil {
		return nil, fmt.Errorf("receipt blobGasUsed: %w", err)
	}
	if out.BlobGasPrice, err = optionalQuantity(raw.BlobGasPrice); err != nil {
		return nil, fmt.Errorf("receipt blobGasPrice: %w", err)
	}
	return wire.MarshalNoEscape(out)
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

func optionalQuantity(value json.RawMessage) (*string, error) {
	if !wire.PresentNonNull(value) {
		return nil, nil
	}
	text, err := wire.CanonicalQuantity(value)
	if err != nil {
		return nil, err
	}
	return &text, nil
}

func optionalAddress(value json.RawMessage) (*string, error) {
	if !wire.PresentNonNull(value) {
		return nil, nil
	}
	text, err := wire.ValidateWireFixedHex(value, 40, "address")
	if err != nil {
		return nil, err
	}
	return &text, nil
}

// canonicalReceipt is the protected eth_getTransactionReceipt object. Field
// order is fixed. Absent optional fields are omitted.
type canonicalReceipt struct {
	TransactionHash   string                `json:"transactionHash"`
	TransactionIndex  string                `json:"transactionIndex"`
	BlockHash         string                `json:"blockHash"`
	BlockNumber       string                `json:"blockNumber"`
	From              *string               `json:"from,omitempty"`
	CumulativeGasUsed string                `json:"cumulativeGasUsed"`
	GasUsed           string                `json:"gasUsed"`
	EffectiveGasPrice *string               `json:"effectiveGasPrice,omitempty"`
	LogsBloom         string                `json:"logsBloom"`
	Logs              []canonicalReceiptLog `json:"logs"`
	Status            *string               `json:"status,omitempty"`
	Root              *string               `json:"root,omitempty"`
	Type              *string               `json:"type,omitempty"`
	ContractAddress   *string               `json:"contractAddress,omitempty"`
	To                *string               `json:"to,omitempty"`
	BlobGasUsed       *string               `json:"blobGasUsed,omitempty"`
	BlobGasPrice      *string               `json:"blobGasPrice,omitempty"`
}

// canonicalReceiptLog is the shared protected log object; the receipt log's
// JSON shape is byte-identical to the eth_getLogs log, so the field set and
// its fixed order live once in the log package.
type canonicalReceiptLog = log.CanonicalLog
