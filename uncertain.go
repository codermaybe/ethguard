package ethguard

import (
	"encoding/json"

	internaluncertain "github.com/codermaybe/ethguard/internal/uncertain"
)

// UncertainResult is an immutable snapshot of one call's collected source
// evidence: the retained raw responses, their top-level field expansion, and
// the retention counters. It is delivered on CallResult.Uncertain when the
// call ran with WithSourceEvidence and a consumer (a bound ResultCapture, or
// the native result itself) existed. Every slice and byte in a delivered
// snapshot is owned by the receiver — mutating one can never reach the
// capture or another copy.
type UncertainResult struct {
	Fields    []UncertainField
	Raw       []UncertainResponse
	Bytes     int
	Limit     int
	Dropped   uint64
	Truncated bool
}

// UncertainField is one source's unnormalized value for a top-level response
// field.
type UncertainField = internaluncertain.Field

// UncertainResponse is one source's original response or error.
type UncertainResponse = internaluncertain.Response

// cloneUncertainResult isolates a snapshot's nested value bytes.
func cloneUncertainResult(r UncertainResult) UncertainResult {
	r.Fields = cloneUncertainFields(r.Fields)
	raw := make([]UncertainResponse, len(r.Raw))
	copy(raw, r.Raw) // copy from the original backing before it is replaced
	r.Raw = raw
	for i := range r.Raw {
		r.Raw[i].Value = append(json.RawMessage(nil), r.Raw[i].Value...)
	}
	return r
}

// FieldsFor returns only the retained entries matching path; every returned
// value is an independent copy.
func (r UncertainResult) FieldsFor(path string) []UncertainField {
	if path == "" {
		return cloneUncertainFields(r.Fields)
	}
	out := make([]UncertainField, 0)
	for _, item := range r.Fields {
		if item.Path == path {
			out = append(out, item)
		}
	}
	return cloneUncertainFields(out)
}

func cloneUncertainFields(in []UncertainField) []UncertainField {
	out := make([]UncertainField, len(in))
	for i, item := range in {
		out[i] = item
		out[i].Value = append(json.RawMessage(nil), item.Value...)
	}
	return out
}
