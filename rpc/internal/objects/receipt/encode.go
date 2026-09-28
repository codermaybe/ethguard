package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"

	"ethguard/rpc/internal/wire"
)

// Encode checks one eth_getTransactionReceipt result and returns the canonical
// JSON of the protected fields. JSON null stays null. from is removed from
// the comparison key and does not enter the value. contractAddress, to,
// blobGasUsed, and blobGasPrice are omitted when absent. A JSON null
// contractAddress is omitted. An empty logs array stays [].
//
// Admission is section 4.2 of return-value-design.md. Fields retained by the
// comparison mask enter the value, except from. Section 14.3 keeps from
// outside both the comparison key and the protected value. logsBloom must
// equal CreateBloom(logs).
// Addresses and hashes are lowercase. Quantities are canonical QUANTITY.
// Log data keeps leading zeros. Array order is unchanged. JSON is compact,
// HTML escaping is off, and field order is the encoder struct order.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("null")) {
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
	if err := Validate(trimmed); err != nil {
		return nil, err
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
	if out.TransactionIndex, err = canonicalQuantity(raw.TransactionIndex); err != nil {
		return nil, fmt.Errorf("receipt transactionIndex: %w", err)
	}
	if out.BlockHash, err = wire.ValidateWireFixedHex(raw.BlockHash, 64, "hash"); err != nil {
		return nil, fmt.Errorf("receipt blockHash: %w", err)
	}
	if out.BlockNumber, err = canonicalQuantity(raw.BlockNumber); err != nil {
		return nil, fmt.Errorf("receipt blockNumber: %w", err)
	}
	if out.CumulativeGasUsed, err = canonicalQuantity(raw.CumulativeGasUsed); err != nil {
		return nil, fmt.Errorf("receipt cumulativeGasUsed: %w", err)
	}
	if out.GasUsed, err = canonicalQuantity(raw.GasUsed); err != nil {
		return nil, fmt.Errorf("receipt gasUsed: %w", err)
	}
	if out.EffectiveGasPrice, err = optionalQuantity(raw.EffectiveGasPrice); err != nil {
		return nil, fmt.Errorf("receipt effectiveGasPrice: %w", err)
	}
	if out.LogsBloom, err = wire.ValidateWireFixedHex(raw.LogsBloom, 512, "logsBloom"); err != nil {
		return nil, fmt.Errorf("receipt logsBloom: %w", err)
	}
	if PresentNonNull(raw.Status) {
		status, err := canonicalQuantity(raw.Status)
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
	return marshal(out)
}

func encodeReceiptLog(value json.RawMessage) (canonicalReceiptLog, error) {
	var raw struct {
		Address          json.RawMessage   `json:"address"`
		Topics           []json.RawMessage `json:"topics"`
		Data             json.RawMessage   `json:"data"`
		BlockNumber      json.RawMessage   `json:"blockNumber"`
		TransactionHash  json.RawMessage   `json:"transactionHash"`
		TransactionIndex json.RawMessage   `json:"transactionIndex"`
		BlockHash        json.RawMessage   `json:"blockHash"`
		LogIndex         json.RawMessage   `json:"logIndex"`
		Removed          json.RawMessage   `json:"removed"`
	}
	if err := json.Unmarshal(value, &raw); err != nil {
		return canonicalReceiptLog{}, fmt.Errorf("expected an object")
	}
	var out canonicalReceiptLog
	var err error
	if out.Address, err = wire.ValidateWireFixedHex(raw.Address, 40, "address"); err != nil {
		return canonicalReceiptLog{}, err
	}
	if raw.Topics == nil {
		return canonicalReceiptLog{}, fmt.Errorf("missing topics")
	}
	out.Topics = make([]string, len(raw.Topics))
	for i, topic := range raw.Topics {
		out.Topics[i], err = wire.ValidateWireFixedHex(topic, 64, "hash")
		if err != nil {
			return canonicalReceiptLog{}, fmt.Errorf("topics[%d]: %w", i, err)
		}
	}
	if out.Data, err = wire.NormalizeWireData(raw.Data); err != nil {
		return canonicalReceiptLog{}, fmt.Errorf("data: %w", err)
	}
	for _, item := range []struct {
		name  string
		value json.RawMessage
		dest  *string
	}{
		{"blockNumber", raw.BlockNumber, &out.BlockNumber},
		{"transactionIndex", raw.TransactionIndex, &out.TransactionIndex},
		{"logIndex", raw.LogIndex, &out.LogIndex},
	} {
		text, err := canonicalQuantity(item.value)
		if err != nil {
			return canonicalReceiptLog{}, fmt.Errorf("%s: %w", item.name, err)
		}
		*item.dest = text
	}
	if out.TransactionHash, err = wire.ValidateWireFixedHex(raw.TransactionHash, 64, "hash"); err != nil {
		return canonicalReceiptLog{}, fmt.Errorf("transactionHash: %w", err)
	}
	if out.BlockHash, err = wire.ValidateWireFixedHex(raw.BlockHash, 64, "hash"); err != nil {
		return canonicalReceiptLog{}, fmt.Errorf("blockHash: %w", err)
	}
	if len(bytes.TrimSpace(raw.Removed)) == 0 {
		return canonicalReceiptLog{}, fmt.Errorf("missing removed")
	}
	if err := json.Unmarshal(raw.Removed, &out.Removed); err != nil {
		return canonicalReceiptLog{}, fmt.Errorf("removed must be boolean")
	}
	return out, nil
}

func optionalQuantity(value json.RawMessage) (*string, error) {
	if !PresentNonNull(value) {
		return nil, nil
	}
	text, err := canonicalQuantity(value)
	if err != nil {
		return nil, err
	}
	return &text, nil
}

func optionalAddress(value json.RawMessage) (*string, error) {
	if !PresentNonNull(value) {
		return nil, nil
	}
	text, err := wire.ValidateWireFixedHex(value, 40, "address")
	if err != nil {
		return nil, err
	}
	return &text, nil
}

func canonicalQuantity(value json.RawMessage) (string, error) {
	decimal, err := wire.NormalizeWireQuantity(value)
	if err != nil {
		return "", err
	}
	number, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		return "", fmt.Errorf("invalid quantity %q", decimal)
	}
	return "0x" + number.Text(16), nil
}

func marshal(value any) (json.RawMessage, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// canonicalReceipt is the protected eth_getTransactionReceipt object. Field
// order is fixed. from is not a field. Absent optional fields are omitted.
type canonicalReceipt struct {
	TransactionHash   string                `json:"transactionHash"`
	TransactionIndex  string                `json:"transactionIndex"`
	BlockHash         string                `json:"blockHash"`
	BlockNumber       string                `json:"blockNumber"`
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

type canonicalReceiptLog struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      string   `json:"blockNumber"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	BlockHash        string   `json:"blockHash"`
	LogIndex         string   `json:"logIndex"`
	Removed          bool     `json:"removed"`
}
