package methods

import (
	"encoding/json"
	"sort"

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

// Projection is the result of projecting a generic observation into one
// method's compile-time standard field set. Extensions remain source-backed
// common observations; they are never synthesized from conflicting values.
type Projection struct {
	Core       []rpc.FieldObservation
	Extensions []rpc.FieldObservation
	Residuals  []rpc.FieldObservation
	Missing    []string
	Conflicts  []rpc.ConflictField
}

type standardProjector struct{ fields []StandardField }

// NewStandardProjector builds the reusable method projector used by the
// aggregator assembly map. It owns a copy of the field declaration.
func NewStandardProjector(fields []StandardField) rpc.ObservationProjector {
	return standardProjector{fields: append([]StandardField(nil), fields...)}
}

func (p standardProjector) ProjectObservation(value json.RawMessage, observed rpc.CommonObservation) (rpc.ExtensionSet, error) {
	return ProjectStandard(observed, p.fields).Carrier(value)
}

// ProjectStandard applies a method's standard field declaration to a generic
// observation. A common field is core only when its path is declared. Common
// fields outside the declaration become extensions. Required fields absent
// from Common are reported in Missing; conflicts are preserved for the method
// policy to decide whether to fail, retry, or degrade.
func ProjectStandard(observation rpc.CommonObservation, fields []StandardField) Projection {
	declared := make(map[string]StandardField, len(fields))
	for _, field := range fields {
		declared[field.Path] = field
	}

	result := Projection{
		Conflicts: append([]rpc.ConflictField(nil), observation.Conflicts...),
		Residuals: append([]rpc.FieldObservation(nil), observation.Residuals...),
	}
	seen := make(map[string]struct{}, len(observation.Common))
	for _, field := range observation.Common {
		seen[field.Path] = struct{}{}
		if _, ok := declared[field.Path]; ok {
			result.Core = append(result.Core, field)
		} else {
			result.Extensions = append(result.Extensions, field)
		}
	}
	for _, field := range fields {
		if field.Required {
			if _, ok := seen[field.Path]; !ok {
				result.Missing = append(result.Missing, field.Path)
			}
		}
	}
	sort.Slice(result.Core, func(i, j int) bool { return result.Core[i].Path < result.Core[j].Path })
	sort.Slice(result.Extensions, func(i, j int) bool { return result.Extensions[i].Path < result.Extensions[j].Path })
	sort.Strings(result.Missing)
	sort.Slice(result.Conflicts, func(i, j int) bool { return result.Conflicts[i].Path < result.Conflicts[j].Path })
	return result
}

// Carrier binds the non-core portion of a projection to the selected
// core response. The returned fields retain their evidence level and source
// identity; no extension is interpreted as a standard Ethereum field.
func (p Projection) Carrier(coreValue []byte) (rpc.ExtensionSet, error) {
	return rpc.NewExtensionSet(coreValue, p.Extensions, p.Residuals, p.Conflicts)
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
