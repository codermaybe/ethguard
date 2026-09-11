package ethguard

import (
	"context"
	"encoding/json"

	internaluncertain "ethguard/internal/uncertain"
)

// UncertainCapture collects untrusted upstream material for one logical call.
// It is opt-in and invocation-scoped: use a fresh capture for every call.
// Reading the capture never changes the primary result or error.
type UncertainCapture struct {
	inner *internaluncertain.Capture
}

// UncertainField is one source's unnormalized value for a top-level response field.
type UncertainField = internaluncertain.Field

// UncertainResponse is one source's original response or error.
type UncertainResponse = internaluncertain.Response

// UncertainResult is an immutable snapshot from one capture.
type UncertainResult struct {
	Fields    []UncertainField
	Raw       []UncertainResponse
	Bytes     int
	Limit     int
	Dropped   uint64
	Truncated bool
}

// WithUncertain enables collection for one logical invocation.
func WithUncertain(ctx context.Context) (context.Context, *UncertainCapture) {
	inner := internaluncertain.New()
	return internaluncertain.WithContext(ctx, inner), &UncertainCapture{inner: inner}
}

// Result returns a defensive snapshot of all retained fields and responses.
func (c *UncertainCapture) Result() UncertainResult {
	if c == nil || c.inner == nil {
		return UncertainResult{}
	}
	snapshot := c.inner.Snapshot()
	return UncertainResult{
		Fields:    snapshot.Fields,
		Raw:       snapshot.Raw,
		Bytes:     snapshot.Bytes,
		Limit:     snapshot.Limit,
		Dropped:   snapshot.Dropped,
		Truncated: snapshot.Truncated,
	}
}

// Snapshot is an alias for Result.
func (c *UncertainCapture) Snapshot() UncertainResult { return c.Result() }

// Uncertain is an alias for Result.
func (c *UncertainCapture) Uncertain() UncertainResult { return c.Result() }

// FieldsFor returns only the retained entries matching path.
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

// Fields returns all retained fields for path from the capture.
func (c *UncertainCapture) Fields(path string) []UncertainField {
	return c.Result().FieldsFor(path)
}

// Responses returns all retained original response/error records.
func (c *UncertainCapture) Responses() []UncertainResponse {
	return c.Result().Raw
}

func cloneUncertainFields(in []UncertainField) []UncertainField {
	out := make([]UncertainField, len(in))
	for i, item := range in {
		out[i] = item
		out[i].Value = append(json.RawMessage(nil), item.Value...)
	}
	return out
}
