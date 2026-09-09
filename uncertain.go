package ethguard

import (
	"context"
	"encoding/json"

	internaluncertain "ethguard/internal/uncertain"
	"ethguard/rpc"
)

// UncertainCapture collects untrusted upstream material for one logical call.
// It is opt-in and invocation-scoped: use a fresh capture for every call.
// Reading the capture never changes the primary result or error.
type UncertainCapture struct {
	inner *internaluncertain.Capture
}

// UncertainField is one source's unnormalized value for a top-level response
// field. Equal values from different sources remain separate entries.
type UncertainField struct {
	Stage  string
	Method string
	Source rpc.SourceRef
	Path   string
	Value  json.RawMessage
}

// UncertainResponse is one source's original response or error. Value is a
// defensive copy and may be truncated when the retention budget is exceeded.
type UncertainResponse struct {
	Stage     string
	Method    string
	Source    rpc.SourceRef
	Value     json.RawMessage
	Error     string
	Truncated bool
}

// UncertainResult is an immutable snapshot from one capture. Its slices and
// raw byte values are newly allocated on every Result call, so modifying them
// cannot mutate the capture or another reader's result.
type UncertainResult struct {
	Fields    []UncertainField
	Raw       []UncertainResponse
	Bytes     int
	Limit     int
	Dropped   uint64
	Truncated bool
}

// WithUncertain enables collection for one logical invocation. The normal
// method signatures stay unchanged; after the call returns, read capture via
// capture.Result(), capture.Fields(path), or capture.Responses().
func WithUncertain(ctx context.Context) (context.Context, *UncertainCapture) {
	inner := internaluncertain.New()
	return internaluncertain.WithContext(ctx, inner), &UncertainCapture{inner: inner}
}

// Result returns a defensive snapshot of all retained fields and responses.
// A failed, cancelled, or partially collected call can still have a result.
func (c *UncertainCapture) Result() UncertainResult {
	if c == nil || c.inner == nil {
		return UncertainResult{}
	}
	snapshot := c.inner.Snapshot()
	out := UncertainResult{
		Fields: make([]UncertainField, len(snapshot.Entries)),
		Raw:    make([]UncertainResponse, len(snapshot.Responses)),
		Bytes:  snapshot.Bytes, Limit: snapshot.Limit,
		Dropped: snapshot.Dropped, Truncated: snapshot.Truncated,
	}
	for i, item := range snapshot.Entries {
		out.Fields[i] = UncertainField{
			Stage: item.Stage, Method: item.Method,
			Source: rpc.SourceRef{Name: item.Source.Name, URL: item.Source.URL, ID: item.Source.ID},
			Path:   item.Path, Value: append(json.RawMessage(nil), item.Value...),
		}
	}
	for i, item := range snapshot.Responses {
		out.Raw[i] = UncertainResponse{
			Stage: item.Stage, Method: item.Method,
			Source: rpc.SourceRef{Name: item.Source.Name, URL: item.Source.URL, ID: item.Source.ID},
			Value:  append(json.RawMessage(nil), item.Value...), Error: item.Error, Truncated: item.Truncated,
		}
	}
	return out
}

// Snapshot is an alias for Result for callers that prefer snapshot wording.
func (c *UncertainCapture) Snapshot() UncertainResult { return c.Result() }

// Uncertain is an alias for Result and intentionally returns a copy.
func (c *UncertainCapture) Uncertain() UncertainResult { return c.Result() }

// Fields returns only the retained entries for path. A blank path returns all
// fields. The returned values are independent defensive copies.
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
	r := c.Result()
	out := make([]UncertainResponse, len(r.Raw))
	for i, item := range r.Raw {
		out[i] = item
		out[i].Value = append(json.RawMessage(nil), item.Value...)
	}
	return out
}

func cloneUncertainFields(in []UncertainField) []UncertainField {
	out := make([]UncertainField, len(in))
	for i, item := range in {
		out[i] = item
		out[i].Value = append(json.RawMessage(nil), item.Value...)
	}
	return out
}
