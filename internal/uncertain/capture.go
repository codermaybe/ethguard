// Package uncertain implements the invocation-scoped source-evidence
// collector: the opt-in, budget-bounded retention of raw upstream material
// during one logical call. Collection never changes a business outcome —
// a budget that runs out stops retention only, never a request, a quorum,
// or a vote.
package uncertain

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/codermaybe/ethguard/rpc"
)

// Retention rules (fixed first version): the parameter-configured total byte
// budget bounds what one call retains; each raw response keeps at most
// maxItemBytes bytes; one call keeps at most maxRecords records across Raw
// and Fields; each variable metadata text keeps at most maxMetadataBytes
// bytes. Bytes counts the actually retained raw bytes plus retained variable
// metadata text (UTF-8), duplicate retention billed separately. It is a
// budget metric, not a precise Go-heap measurement.
const (
	// DefaultBudget is the retention budget of the transitional
	// material-only entry; explicit configuration replaces it.
	DefaultBudget = 1 << 20
	maxItemBytes  = 64 << 10
	maxRecords    = 256
	// maxMetadataBytes caps one variable metadata text (an error string, a
	// source identity component).
	maxMetadataBytes = 4 << 10
)

type configKey struct{}
type collectorKey struct{}

// Config is the immutable retention configuration of one call.
type Config struct {
	// BudgetBytes is the total material byte budget for one public call.
	// Every operation consumes its own budget; internal reference rounds,
	// business fan-outs, admission dependencies, and broadcast material
	// share this call's budget, different calls do not.
	BudgetBytes int
}

// WithConfig puts the immutable collection configuration on a derived
// context. The last configuration on a derivation chain wins.
func WithConfig(ctx context.Context, c Config) context.Context {
	return context.WithValue(ctx, configKey{}, c)
}

// ConfigFromContext reports the effective configuration for ctx.
func ConfigFromContext(ctx context.Context) (Config, bool) {
	if ctx == nil {
		return Config{}, false
	}
	c, _ := ctx.Value(configKey{}).(Config)
	return c, c.BudgetBytes > 0
}

// WithCollector installs the per-call collector on ctx. A nil collector
// means no consumer: recording stays off while configuration may stay on.
func WithCollector(ctx context.Context, c *Capture) context.Context {
	return context.WithValue(ctx, collectorKey{}, c)
}

// CollectorFromContext returns the collector installed for this call, or nil
// when the call runs without material collection.
func CollectorFromContext(ctx context.Context) *Capture {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(collectorKey{}).(*Capture)
	return c
}

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

// InvocationCapture adapts the collector to the rpc.InvocationCapture seam:
// the one production binding both the aggregator pipeline and the runtime's
// facade establishment rounds record through.
func (c *Capture) InvocationCapture() rpc.InvocationCapture { return invocationCapture{c} }

type invocationCapture struct{ c *Capture }

func (a invocationCapture) Record(stage, method string, source rpc.SourceRef, value []byte, errText string) {
	a.c.Record(stage, method, source, value, errText)
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

// Capture is safe for concurrent recording and reading across one logical
// invocation. It owns the retention budget of exactly one call.
type Capture struct {
	mu sync.Mutex

	cfg       Config
	fields    []Field
	responses []Response
	bytes     int
	dropped   uint64
	truncated bool
	records   int
}

// NewCollector builds the collector for one call under cfg. Callers must
// have validated cfg.BudgetBytes > 0.
func NewCollector(cfg Config) *Capture {
	return &Capture{cfg: cfg}
}

// metadataText caps one variable metadata text at the metadata bound and
// reports the retained byte count plus whether truncation happened — a cut
// text is a partially retained entry and must surface as Truncated. The
// returned view is borrowed; clone it only after retention is approved.
func metadataText(text string) (string, int, bool) {
	if len(text) <= maxMetadataBytes {
		return text, len(text), false
	}
	// Cut at a UTF-8 boundary so a retained text is always valid UTF-8.
	cut := maxMetadataBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], cut, true
}

// metadataRef caps a source identity and reports the retained byte count
// and truncation.
func metadataRef(source rpc.SourceRef) (rpc.SourceRef, int, bool) {
	name, n1, t1 := metadataText(source.Name)
	url, n2, t2 := metadataText(source.URL)
	id, n3, t3 := metadataText(source.ID)
	return rpc.SourceRef{Name: name, URL: url, ID: id}, n1 + n2 + n3, t1 || t2 || t3
}

// Record retains one logical entry: the raw response (or error) record and,
// for a value-bearing response, its bounded top-level field expansion. The
// whole entry's metadata — stage, method, source identity, error text — is
// billed before any payload copy, so Bytes can never exceed Limit: an entry
// whose metadata alone does not fit the remaining budget drops whole. The
// budget and the record limit stop retention and field expansion while the
// business collection continues unchanged.
func (c *Capture) Record(stage, method string, source rpc.SourceRef, value []byte, errText string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	source, sourceBytes, sourceTrunc := metadataRef(source)
	errText, errBytes, errTrunc := metadataText(errText)
	stageText, stageBytes, stageTrunc := metadataText(stage)
	methodText, methodBytes, methodTrunc := metadataText(method)

	// The entry's metadata is retained text: bill it before the payload and
	// refuse the whole entry when even the metadata cannot fit.
	metaBytes := sourceBytes + errBytes + stageBytes + methodBytes
	if metaBytes > c.cfg.BudgetBytes-c.bytes {
		c.dropped++
		c.truncated = true
		return
	}

	// At the record limit only counters move; no empty records.
	if c.records >= maxRecords {
		c.dropped++
		c.truncated = true
		return
	}

	// Bill the metadata, then retain the payload within what remains.
	c.bytes += metaBytes
	raw, rawTruncated := c.retain(value)
	if raw == nil && len(value) > 0 && errText == "" {
		// No payload fit and there is no error text to keep: nothing of
		// this entry survives, so the metadata billing rolls back.
		c.bytes -= metaBytes
		c.dropped++
		c.truncated = true
		return
	}
	// Detach even short substrings: their caller-owned backing storage can
	// be arbitrarily large. Only approved, capped text is copied.
	stageText, methodText = strings.Clone(stageText), strings.Clone(methodText)
	source = rpc.SourceRef{Name: strings.Clone(source.Name), URL: strings.Clone(source.URL), ID: strings.Clone(source.ID)}
	errText = strings.Clone(errText)
	entryTruncated := rawTruncated || sourceTrunc || errTrunc || stageTrunc || methodTrunc
	c.responses = append(c.responses, Response{
		Stage:     stageText,
		Method:    methodText,
		Source:    source,
		Value:     raw,
		Error:     errText,
		Truncated: entryTruncated,
	})
	c.records++
	if entryTruncated {
		// The logical entry did not survive whole: one drop, no matter how
		// many of its parts were cut.
		c.dropped++
		c.truncated = true
	}

	if errText != "" || len(value) == 0 {
		return
	}
	c.expandFields(stageText, methodText, source, raw)
}

// expandFields examines only the retained raw prefix (at most maxItemBytes).
// Only complete members become Fields; a cut or malformed tail stays in Raw.
// The walker checks capacity before scanning each member, and bounds key
// decoding separately. No discarded tail is decoded to count its members.
func (c *Capture) expandFields(stage, method string, source rpc.SourceRef, value []byte) {
	baseMeta := len(stage) + len(method) + len(source.Name) + len(source.URL) + len(source.ID)
	complete := scanObjectMembers(value, func() int {
		remaining := c.cfg.BudgetBytes - c.bytes - baseMeta
		if c.records >= maxRecords || remaining <= 0 {
			return -1
		}
		// Reserve at least one byte for the member value. JSON escapes use
		// at most six wire bytes per decoded UTF-8 byte.
		return min(maxMetadataBytes, remaining-1)
	}, func(key string, member []byte) bool {
		path, pathBytes, pathTrunc := metadataText(key)
		fieldMeta := baseMeta + pathBytes
		if fieldMeta >= c.cfg.BudgetBytes-c.bytes {
			return false
		}
		c.bytes += fieldMeta
		field, fieldTruncated := c.retain(member)
		c.fields = append(c.fields, Field{
			Stage: stage, Method: method, Source: source,
			Path: strings.Clone(path), Value: field,
		})
		c.records++
		if fieldTruncated || pathTrunc {
			c.dropped++
			c.truncated = true
		}
		return true
	})
	if !complete {
		// Count the abandoned tail once, without walking it.
		c.dropped++
		c.truncated = true
	}
}

// scanObjectMembers visits complete members of a bounded raw prefix. Before
// scanning a member, keyBudget returns its decoded metadata allowance, or -1
// to stop. A key's wire scan and decode are bounded by six times that allowance
// plus quotes. Oversized keys stop expansion; raw evidence is unaffected.
// False means that some tail could not be expanded (capacity, size, or syntax).
func scanObjectMembers(data []byte, keyBudget func() int, visit func(key string, val []byte) bool) bool {
	i := 0
	skipWS := func() {
		for i < len(data) {
			switch data[i] {
			case ' ', '\t', '\n', '\r':
				i++
			default:
				return
			}
		}
	}
	skipWS()
	if i >= len(data) || data[i] != '{' {
		return true // Non-object responses have no top-level fields.
	}
	i++
	skipWS()
	if i < len(data) && data[i] == '}' {
		return true
	}
	budget := keyBudget()
	if budget < 0 {
		return false
	}
	for {
		if i >= len(data) || data[i] != '"' {
			return false
		}
		keyStart := i
		keyEnd := min(len(data), i+6*budget+2)
		if !scanJSONString(data[:keyEnd], &i) {
			return false
		}
		var key string
		if err := json.Unmarshal(data[keyStart:i], &key); err != nil {
			return false
		}
		skipWS()
		if i >= len(data) || data[i] != ':' {
			return false
		}
		i++
		skipWS()
		valStart := i
		if !scanJSONValue(data, &i) || !json.Valid(data[valStart:i]) {
			return false
		}
		valEnd := i
		skipWS()
		// A raw prefix ending in a number can itself be valid JSON while
		// still cutting that number in half. Require the member delimiter
		// before publishing any field from a possibly truncated prefix.
		if i >= len(data) || (data[i] != ',' && data[i] != '}') {
			return false
		}
		if !visit(key, data[valStart:valEnd]) {
			return false
		}
		// Inspect only delimiters before checking capacity for the next
		// member. Whitespace is bounded by the retained raw prefix too.
		if i < len(data) && data[i] == '}' {
			return true
		}
		budget = keyBudget()
		if budget < 0 {
			return false
		}
		skipWS()
		if i >= len(data) {
			return false
		}
		switch data[i] {
		case ',':
			i++
			skipWS()
		case '}':
			return true
		default:
			return false
		}
	}
}

// scanJSONString advances i from the opening quote past the closing one,
// honoring backslash escapes. Malformed or truncated strings fail.
func scanJSONString(data []byte, i *int) bool {
	d := *i + 1
	for d < len(data) {
		switch data[d] {
		case '\\':
			d += 2 // skip the escape pair; a trailing lone backslash fails below
			continue
		case '"':
			*i = d + 1
			return true
		}
		d++
	}
	return false
}

// scanJSONValue advances i past one complete JSON value (object, array,
// string, or literal), tracking nesting only where it can occur.
func scanJSONValue(data []byte, i *int) bool {
	d := *i
	if d >= len(data) {
		return false
	}
	switch data[d] {
	case '"':
		return scanJSONString(data, i)
	case '{', '[':
		depth := 0
		for d < len(data) {
			switch data[d] {
			case '"':
				if !scanJSONString(data, &d) {
					return false
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					*i = d + 1
					return true
				}
			}
			d++
		}
		return false
	default:
		// A literal: number, true, false, or null — advance to the first
		// structural delimiter. A malformed fragment keeps its raw bytes as
		// evidence; the scan never rewrites them.
		for d < len(data) {
			switch data[d] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				if d > *i {
					*i = d
					return true
				}
				return false
			}
			d++
		}
		if d > *i {
			*i = d
			return true
		}
		return false
	}
}

// retain copies at most the item and budget bounds of value into a new
// slice. It reports whether the copy is a partial retention; the drop and
// truncation accounting belongs to the caller — one logical entry counts at
// most once, no matter how many of its parts were cut.
func (c *Capture) retain(value []byte) ([]byte, bool) {
	if len(value) == 0 {
		return nil, false
	}
	remaining := c.cfg.BudgetBytes - c.bytes
	if remaining <= 0 {
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
		return nil, true
	}
	out := append([]byte(nil), value[:n]...)
	c.bytes += n
	return out, truncated || n < len(value)
}

// Snapshot returns a defensive snapshot copy: the caller owns every byte and
// slice it receives, and later records cannot reach into it.
func (c *Capture) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Snapshot{
		Bytes:     c.bytes,
		Limit:     c.cfg.BudgetBytes,
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
