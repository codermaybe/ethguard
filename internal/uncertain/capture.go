// Package uncertain implements the invocation-scoped Uncertain collector.
// It records raw unnormalized upstream data during request execution.
package uncertain

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/codermaybe/ethguard/rpc"
)

const (
	defaultBudget = 1 << 20
	maxItemBytes  = 64 << 10
)

type contextKey struct{}
type scopeKey struct{}
type disabledKey struct{}

// Field is one source's unnormalized value for a top-level response field.
type Field struct {
	Stage  string
	Method string
	Source rpc.SourceRef
	Path   string
	Value  json.RawMessage
}

// Response is one source's original raw response or transport error.
type Response struct {
	Stage     string
	Method    string
	Source    rpc.SourceRef
	Value     json.RawMessage
	Error     string
	Truncated bool
}

// Snapshot is the immutable collection snapshot.
type Snapshot struct {
	Fields    []Field
	Raw       []Response
	Bytes     int
	Limit     int
	Dropped   uint64
	Truncated bool
}

// Capture is safe for concurrent recording and reading across one logical invocation.
type Capture struct {
	mu sync.Mutex

	fields    []Field
	responses []Response
	bytes     int
	limit     int
	dropped   uint64
	truncated bool
	active    int
	started   bool
	published bool
}

func New() *Capture { return &Capture{limit: defaultBudget} }

func WithContext(ctx context.Context, c *Capture) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

func FromContext(ctx context.Context) *Capture {
	if ctx == nil {
		return nil
	}
	if disabled, _ := ctx.Value(disabledKey{}).(bool); disabled {
		return nil
	}
	c, _ := ctx.Value(contextKey{}).(*Capture)
	return c
}

// Begin starts a top-level collection or joins an aggregate nested under the
// current call.
func Begin(ctx context.Context) (context.Context, func()) {
	c := FromContext(ctx)
	if c == nil {
		return ctx, func() {}
	}
	nested, _ := ctx.Value(scopeKey{}).(*Capture)
	c.mu.Lock()
	if nested != c {
		if c.started || c.published {
			c.mu.Unlock()
			return context.WithValue(ctx, disabledKey{}, true), func() {}
		}
		c.started = true
	}
	c.active++
	c.mu.Unlock()
	marked := context.WithValue(ctx, scopeKey{}, c)
	return marked, func() {
		c.mu.Lock()
		if c.active > 0 {
			c.active--
		}
		if c.active == 0 {
			c.published = true
		}
		c.mu.Unlock()
	}
}

func (c *Capture) Record(stage, method string, source rpc.SourceRef, value []byte, errText string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.published {
		return
	}
	raw, rawTruncated := c.retain(value)
	c.responses = append(c.responses, Response{
		Stage:     stage,
		Method:    method,
		Source:    source,
		Value:     raw,
		Error:     errText,
		Truncated: rawTruncated,
	})
	if errText != "" || len(value) == 0 {
		return
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field, truncated := c.retain(object[key])
		if field == nil && len(object[key]) > 0 {
			continue
		}
		c.fields = append(c.fields, Field{
			Stage:  stage,
			Method: method,
			Source: source,
			Path:   key,
			Value:  field,
		})
		if truncated {
			break
		}
	}
}

func (c *Capture) retain(value []byte) ([]byte, bool) {
	if len(value) == 0 {
		return nil, false
	}
	remaining := c.limit - c.bytes
	if remaining <= 0 {
		c.dropped++
		c.truncated = true
		return nil, true
	}
	n := len(value)
	truncated := false
	if n > maxItemBytes {
		n = maxItemBytes
		truncated = true
	}
	if n > remaining {
		n = remaining
		truncated = true
	}
	if n <= 0 {
		c.dropped++
		c.truncated = true
		return nil, true
	}
	out := append([]byte(nil), value[:n]...)
	c.bytes += n
	if truncated || n < len(value) {
		c.dropped++
		c.truncated = true
	}
	return out, truncated || n < len(value)
}

// Snapshot returns a defensive snapshot copy.
func (c *Capture) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Snapshot{
		Bytes:     c.bytes,
		Limit:     c.limit,
		Dropped:   c.dropped,
		Truncated: c.truncated,
	}
	out.Fields = make([]Field, len(c.fields))
	for i, item := range c.fields {
		out.Fields[i] = item
		out.Fields[i].Value = append(json.RawMessage(nil), item.Value...)
	}
	out.Raw = make([]Response, len(c.responses))
	for i, item := range c.responses {
		out.Raw[i] = item
		out.Raw[i].Value = append(json.RawMessage(nil), item.Value...)
	}
	return out
}

func (c *Capture) Publish() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.published = true
	c.mu.Unlock()
}
