package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/ethobject/log"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// Receipt projection follows the audited generated model's member inventory.
// Only its declared scalars receive hex spelling compatibility; logs use
// their own protected encoder and provider extensions stay outside the value.
var receiptFields = func() []string {
	t := reflect.TypeOf(ethapi.Receipt{})
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" && name != "logs" {
			names = append(names, name)
		}
	}
	return names
}()

// EncodeBlock encodes one complete eth_getBlockReceipts response using the
// same protected receipt model as Encode. Null is an absent block; [] is an
// empty block. Null or invalid elements invalidate the whole source response.
func EncodeBlock(value json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(value); err != nil {
		return nil, fmt.Errorf("receipt JSON: %w", err)
	}
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
// was checked. PrepareTransaction and PrepareBlock additionally bind receipts
// to the request they answer.
// Addresses and hashes are lowercase. Quantities are canonical QUANTITY.
// Log data keeps leading zeros. Array order is unchanged. JSON is compact,
// HTML escaping is off, and field order is the encoder struct order.
func Encode(value json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(value); err != nil {
		return nil, fmt.Errorf("receipt JSON: %w", err)
	}
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
	logs := make([]log.CanonicalLog, len(rows))
	for i, row := range rows {
		item, err := log.EncodeOne(row)
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
	candidate := make(map[string]json.RawMessage, len(receiptFields)+1)
	for _, name := range receiptFields {
		if raw, present := fields[name]; present {
			candidate[name] = ethcodec.FoldHexSpelling(raw)
		}
	}
	candidate["logs"] = canonicalLogs
	checked, err := ethcodec.MarshalNoEscape(candidate)
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
func checkLogBindings(logs []log.CanonicalLog, transactionHash, blockHash string) error {
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
func canonicalBloom(logs []log.CanonicalLog) string {
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

// Decode checks the canonical receipt, its logs and bloom, and its mined
// identity before returning the private algorithm representation. Root null
// is handled by the caller's query/event policy and is rejected here.
func Decode(value json.RawMessage) (*types.Receipt, error) {
	_, r, err := DecodeWithCanonical(value)
	return r, err
}

// DecodeWithCanonical runs the complete Decode flow once and also returns
// the canonical bytes the decode consumed: Encode's checked output, the same
// bytes the private representation was parsed from. Callers that must bind a
// receipt to its request keep this way one Encode per response — the bytes
// are validated, then handed out, never rebuilt. On failure both returns are
// nil and the error is exactly Decode's.
func DecodeWithCanonical(value json.RawMessage) (json.RawMessage, *types.Receipt, error) {
	canonical, err := Encode(value)
	if err != nil {
		return nil, nil, err
	}
	var r *types.Receipt
	if err := json.Unmarshal(canonical, &r); err != nil || r == nil {
		return nil, nil, fmt.Errorf("receipt: invalid payload")
	}
	if r.TxHash == (common.Hash{}) || r.BlockHash == (common.Hash{}) {
		return nil, nil, fmt.Errorf("receipt: missing transaction or block hash")
	}
	return canonical, r, nil
}
