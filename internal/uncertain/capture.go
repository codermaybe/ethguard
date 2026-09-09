// Package uncertain implements the invocation-scoped Uncertain collector.
// It is intentionally internal: the root package exposes immutable copies of
// its snapshots while the aggregator records raw source responses here.
package uncertain

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

const (
	defaultBudget = 1 << 20
	maxItemBytes  = 64 << 10
)

type contextKey struct{}
type scopeKey struct{}
type disabledKey struct{}

// Source identifies the source associated with one retained item. Strings are
// copied into the immutable snapshot and do not retain pool objects.
type Source struct {
	Name string
	URL  string
	ID   string
}

type Entry struct {
	Stage  string
	Method string
	Source Source
	Path   string
	Value  []byte
}

type RawResponse struct {
	Stage     string
	Method    string
	Source    Source
	Value     []byte
	Error     string
	Truncated bool
}

type Snapshot struct {
	Entries   []Entry
	Responses []RawResponse
	Bytes     int
	Limit     int
	Dropped   uint64
	Truncated bool
}

// Capture is safe for concurrent recording and reading. A capture is one
// logical invocation; a second unrelated top-level call using the same context
// is ignored rather than silently concatenated into the first call.
type Capture struct {
	mu sync.Mutex

	entries   []Entry
	responses []RawResponse
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
// current call (for example, reference establishment). The returned context
// marks nested aggregations so concurrent callers reusing the original context
// cannot accidentally merge their records.
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
			// A reused capture must not silently merge a second top-level
			// invocation. Hide it from the returned context while preserving
			// the caller's ability to continue using the original context.
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

func (c *Capture) Record(stage, method string, source Source, value []byte, errText string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.published {
		return
	}
	// Keep one original response entry per source and stage. The raw bytes are
	// retained before field extraction so malformed payloads remain observable.
	raw, rawTruncated := c.retain(value)
	c.responses = append(c.responses, RawResponse{Stage: stage, Method: method, Source: source, Value: raw, Error: errText, Truncated: rawTruncated})
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
		c.entries = append(c.entries, Entry{Stage: stage, Method: method, Source: source, Path: key, Value: field})
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

// Snapshot returns a complete defensive copy. It is valid both before and
// after publication; after publication it can never change.
func (c *Capture) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Snapshot{Bytes: c.bytes, Limit: c.limit, Dropped: c.dropped, Truncated: c.truncated}
	out.Entries = make([]Entry, len(c.entries))
	for i, item := range c.entries {
		out.Entries[i] = item
		out.Entries[i].Value = append([]byte(nil), item.Value...)
	}
	out.Responses = make([]RawResponse, len(c.responses))
	for i, item := range c.responses {
		out.Responses[i] = item
		out.Responses[i].Value = append([]byte(nil), item.Value...)
	}
	return out
}

// Publish is kept as an explicit operation for callers that finish a phase
// outside the aggregator. Aggregator users should use the done function from
// Begin; late records are rejected after the final done call.
func (c *Capture) Publish() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.published = true
	c.mu.Unlock()
}
