package projection

import (
	"encoding/json"
	"sort"
	"strings"

	"ethguard/rpc"
)

// StandardField declares one method-level field in the semantic projection
// room. Paths use the same JSON Pointer notation produced by
// rpc.AggregateFields. Requiredness is a method contract, not a property of
// the generic observation room.
type StandardField struct {
	Path     string
	Required bool
}

// Projection is the result of projecting an evaluation into one method's
// compile-time standard field set. Extensions contain only accepted
// source-backed values; they are never synthesized from diagnostics.
type Projection struct {
	Core               []rpc.FieldObservation
	AcceptedExtensions []rpc.FieldObservation
	Missing            []string
}

type standardProjector struct{ fields []StandardField }

// NewStandardProjector builds the reusable method projector used by the
// aggregator assembly map. It owns a copy of the field declaration.
func NewStandardProjector(fields []StandardField) rpc.ObservationProjector {
	return standardProjector{fields: append([]StandardField(nil), fields...)}
}

func (p standardProjector) ProjectObservation(value json.RawMessage, evaluation rpc.Evaluation) (rpc.ExtensionSet, error) {
	return ProjectStandard(evaluation, p.fields).Carrier(value)
}

// ProjectStandard classifies already-accepted observations into a method's
// declared fields. It does not admit responses and it does not choose the
// comparison key. An accepted field is core only when its path is declared;
// accepted fields outside the declaration become extensions. Required fields
// absent from Accepted are reported in Missing. Diagnostics stay on the
// evaluation and do not become extensions.
func ProjectStandard(evaluation rpc.Evaluation, fields []StandardField) Projection {
	declared := make(map[string]StandardField, len(fields))
	for _, field := range fields {
		declared[field.Path] = field
	}

	result := Projection{}
	seen := make(map[string]struct{}, len(evaluation.Accepted))
	for _, field := range evaluation.Accepted {
		seen[field.Path] = struct{}{}
		if _, ok := standardFieldForPath(field.Path, declared); ok {
			result.Core = append(result.Core, field)
		} else {
			result.AcceptedExtensions = append(result.AcceptedExtensions, field)
		}
	}
	for _, field := range fields {
		if field.Required {
			if strings.Contains(field.Path, "/*") {
				continue
			}
			if _, ok := seen[field.Path]; !ok {
				result.Missing = append(result.Missing, field.Path)
			}
		}
	}
	sort.Slice(result.Core, func(i, j int) bool { return result.Core[i].Path < result.Core[j].Path })
	sort.Slice(result.AcceptedExtensions, func(i, j int) bool { return result.AcceptedExtensions[i].Path < result.AcceptedExtensions[j].Path })
	sort.Strings(result.Missing)
	return result
}

func standardFieldForPath(path string, fields map[string]StandardField) (StandardField, bool) {
	if field, ok := fields[path]; ok {
		return field, true
	}
	for pattern, field := range fields {
		if matchJSONPointerPattern(pattern, path) {
			return field, true
		}
	}
	return StandardField{}, false
}

func matchJSONPointerPattern(pattern, path string) bool {
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i := range patternParts {
		if patternParts[i] != "*" && patternParts[i] != pathParts[i] {
			return false
		}
	}
	return true
}

// Carrier binds accepted non-core fields to the selected core response. No
// extension is interpreted as a standard Ethereum field.
func (p Projection) Carrier(coreValue []byte) (rpc.ExtensionSet, error) {
	return rpc.NewExtensionSet(coreValue, p.AcceptedExtensions)
}

// TransactionReceiptStandardFields is the compile-time core projection for
// eth_getTransactionReceipt and eth_getBlockReceipts. The alternative
// post-London receipt commitment (status or root) is enforced by receipt
// admission; both fields remain optional in this flat declaration.
var transactionReceiptStandardFields = []StandardField{
	{Path: "/transactionHash", Required: true},
	{Path: "/transactionIndex", Required: true},
	{Path: "/blockHash", Required: true},
	{Path: "/blockNumber", Required: true},
	{Path: "/from", Required: false},
	{Path: "/to", Required: false},
	{Path: "/cumulativeGasUsed", Required: true},
	{Path: "/gasUsed", Required: true},
	{Path: "/contractAddress", Required: false},
	{Path: "/logs", Required: true},
	{Path: "/logsBloom", Required: true},
	{Path: "/status", Required: false},
	{Path: "/root", Required: false},
	{Path: "/effectiveGasPrice", Required: false},
	{Path: "/type", Required: false},
	{Path: "/blobGasUsed", Required: false},
	{Path: "/blobGasPrice", Required: false},
}

// TransactionReceiptStandardFields returns a copy so callers cannot mutate
// the method's compile-time projection for other requests.
func TransactionReceiptStandardFields() []StandardField {
	return append([]StandardField(nil), transactionReceiptStandardFields...)
}

var blockStandardFields = []StandardField{
	{Path: "/hash", Required: true},
	{Path: "/parentHash", Required: true},
	{Path: "/sha3Uncles", Required: true},
	{Path: "/transactionsRoot", Required: true},
	{Path: "/stateRoot", Required: true},
	{Path: "/receiptsRoot", Required: true},
	{Path: "/miner", Required: true},
	{Path: "/difficulty", Required: true},
	{Path: "/number", Required: true},
	{Path: "/gasLimit", Required: true},
	{Path: "/gasUsed", Required: true},
	{Path: "/timestamp", Required: true},
	{Path: "/extraData", Required: true},
	{Path: "/logsBloom", Required: true},
	{Path: "/mixHash", Required: true},
	{Path: "/nonce", Required: true},
	{Path: "/transactions", Required: true},
	{Path: "/uncles", Required: true},
	{Path: "/baseFeePerGas", Required: false},
	{Path: "/withdrawalsRoot", Required: false},
	{Path: "/blobGasUsed", Required: false},
	{Path: "/excessBlobGas", Required: false},
	{Path: "/parentBeaconBlockRoot", Required: false},
	{Path: "/requestsHash", Required: false},
	{Path: "/size", Required: true},
	{Path: "/blockAccessListHash", Required: false},
	{Path: "/withdrawals", Required: false},
}

// BlockStandardFields returns the compile-time core projection for block
// responses. Fork-specific fields remain optional and are admitted by the
// block policy when present.
func BlockStandardFields() []StandardField {
	return append([]StandardField(nil), blockStandardFields...)
}

var transactionStandardFields = []StandardField{
	{Path: "/hash", Required: true},
	{Path: "/nonce", Required: true},
	{Path: "/blockHash", Required: true},
	{Path: "/blockNumber", Required: true},
	{Path: "/transactionIndex", Required: true},
	{Path: "/from", Required: true},
	{Path: "/to", Required: false},
	{Path: "/value", Required: true},
	{Path: "/gas", Required: true},
	{Path: "/gasPrice", Required: false},
	{Path: "/input", Required: true},
	{Path: "/v", Required: true},
	{Path: "/r", Required: true},
	{Path: "/s", Required: true},
	{Path: "/type", Required: true},
	{Path: "/chainId", Required: false},
	{Path: "/accessList", Required: false},
	{Path: "/maxFeePerGas", Required: false},
	{Path: "/maxPriorityFeePerGas", Required: false},
	{Path: "/yParity", Required: false},
	{Path: "/blobVersionedHashes", Required: false},
	{Path: "/maxFeePerBlobGas", Required: false},
	{Path: "/blockTimestamp", Required: true},
	{Path: "/authorizationList", Required: false},
}

// TransactionStandardFields returns the compile-time core projection for
// eth_getTransactionByHash responses.
func TransactionStandardFields() []StandardField {
	return append([]StandardField(nil), transactionStandardFields...)
}

var logsStandardFields = []StandardField{
	{Path: "/*", Required: false},
	{Path: "/*/address", Required: true},
	{Path: "/*/topics", Required: true},
	{Path: "/*/data", Required: true},
	{Path: "/*/blockNumber", Required: true},
	{Path: "/*/transactionHash", Required: true},
	{Path: "/*/transactionIndex", Required: true},
	{Path: "/*/blockHash", Required: true},
	{Path: "/*/logIndex", Required: true},
	{Path: "/*/removed", Required: false},
	{Path: "/*/blockTimestamp", Required: false},
}

// LogsStandardFields describes each log entry in an eth_getLogs result.
func LogsStandardFields() []StandardField {
	return append([]StandardField(nil), logsStandardFields...)
}

var feeHistoryStandardFields = []StandardField{
	{Path: "/oldestBlock", Required: true},
	{Path: "/baseFeePerGas/*", Required: true},
	{Path: "/gasUsedRatio/*", Required: true},
	{Path: "/reward/*/*", Required: false},
	{Path: "/baseFeePerBlobGas/*", Required: false},
	{Path: "/blobGasUsedRatio/*", Required: false},
}

// FeeHistoryStandardFields describes the stable object and array members of
// eth_feeHistory. Array lengths are validated by the method policy, not by
// the generic projection.
func FeeHistoryStandardFields() []StandardField {
	return append([]StandardField(nil), feeHistoryStandardFields...)
}

// ScalarStandardProjector projects scalar result methods such as balance and
// call. The root JSON path is the scalar value itself.
func ScalarStandardProjector() rpc.ObservationProjector {
	return NewStandardProjector([]StandardField{{Path: "", Required: true}})
}
