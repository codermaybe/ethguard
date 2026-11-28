// Package ethadapter presents EthGuard through go-ethereum business types.
// Its Client shares the supplied core's execution and lifecycle. Conversion
// failures are adapter errors; they do not rewrite the core's recorded result.
package ethadapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
)

// Client is a go-ethereum-shaped view of one EthGuard execution owner.
type Client struct {
	core   *ethguard.Client
	audit  func(context.Context, AuditEvent)
	logger *slog.Logger
}

// Option configures observation, never execution or conversion policy.
type Option func(*Client)

// WithAudit installs a synchronous observer, taking precedence over WithLogger.
// Observers must return promptly and must not call Close from a subscription
// callback. Panics are isolated and never change the business result.
func WithAudit(fn func(context.Context, AuditEvent)) Option {
	return func(c *Client) { c.audit = fn }
}

// WithLogger records call outcomes when no audit callback is configured.
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.logger = l } }

// New shares core's lifecycle. A nil core is rejected by calls before execution.
func New(core *ethguard.Client, opts ...Option) *Client {
	c := &Client{core: core}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// Dial constructs a lazy client from one or more immutable endpoints.
func Dial(endpoints ...string) (*Client, error) {
	core, err := ethguard.Dial(endpoints...)
	if err != nil {
		return nil, err
	}
	return New(core), nil
}

// DialContext checks ctx during construction without retaining it as a lifetime.
func DialContext(ctx context.Context, endpoints ...string) (*Client, error) {
	core, err := ethguard.DialContext(ctx, endpoints...)
	if err != nil {
		return nil, err
	}
	return New(core), nil
}

// Core returns the shared execution owner and its full evidence surface.
func (c *Client) Core() *ethguard.Client {
	if c == nil {
		return nil
	}
	return c.core
}

// Close closes the shared core and its upstream connections.
func (c *Client) Close() {
	if c != nil && c.core != nil {
		c.core.Close()
	}
}

// AuditEvent separates core execution from adapter conversion. CallResult.Err
// and CoreErr describe the same core call; AdapterErr is never inserted there.
type AuditEvent struct {
	Subscription     *SubscriptionAudit
	Method           string
	Duration         time.Duration
	CallResult       ethguard.CallResult
	ConversionReport ConversionReport
	CoreErr          error
	AdapterErr       error
	EvidenceScope    EvidenceScope
}

// EvidenceScope describes observed carriers, not proof of absent extensions.
// In particular, Forwarded with ExtensionsObserved=false does not prove that
// a provider supplied no fields outside the public ethapi model.
type EvidenceScope struct {
	Forwarded              bool
	ExtensionsObserved     bool
	SourceEvidenceObserved bool
}

func (c *Client) observing() bool { return c != nil && (c.audit != nil || c.logger != nil) }

func (c *Client) observe(ctx context.Context, method string, started time.Time, result ethguard.CallResult, report ConversionReport, coreErr, adapterErr error) {
	if !c.observing() {
		return
	}
	// Observation is not part of the operation's business conclusion. This also
	// protects successful broadcasts from being made retryable by a bad sink.
	defer func() { _ = recover() }()
	scope := EvidenceScope{SourceEvidenceObserved: result.Uncertain != nil}
	if result.Decision != nil {
		scope.Forwarded = result.Decision.Status == rpc.Forwarded
		scope.ExtensionsObserved = result.Decision.Extensions != nil
		if result.Decision.Extensions != nil && len(result.Decision.Extensions.Fields) != 0 {
			report.add("Decision.Extensions", "provider extensions remain in CallResult and are not embedded in geth objects")
		}
	}
	event := AuditEvent{Method: method, Duration: time.Since(started), CallResult: result, ConversionReport: report, CoreErr: coreErr, AdapterErr: adapterErr, EvidenceScope: scope}
	if c.audit != nil {
		c.audit(ctx, event)
		return
	}
	attrs := []slog.Attr{slog.String("method", method), slog.Duration("duration", event.Duration), slog.Any("core_error", coreErr), slog.Any("adapter_error", adapterErr), slog.Any("omissions", report.Omissions), slog.Any("evidence_scope", scope)}
	if d := result.Decision; d != nil {
		attrs = append(attrs, slog.String("status", d.Status.String()), slog.Int("support", len(d.Support)), slog.Int("basis", d.Adjudication.Basis))
	}
	if d := result.WriteDecision; d != nil {
		support := 0
		for _, source := range d.Sources {
			if d.Hash != "" && source.Hash == d.Hash {
				support++
			}
		}
		attrs = append(attrs, slog.String("write_status", d.Status.String()), slog.Int("support", support), slog.Int("basis", d.Adjudication.Basis), slog.String("transaction_hash", d.Hash))
	}
	c.logger.LogAttrs(ctx, slog.LevelInfo, "ethadapter call", attrs...)
}

// execute deliberately does not install WithResult: an existing outer capture
// must see the native operation's own conclusion even if conversion then fails.
func execute[A, B any](c *Client, ctx context.Context, method string, preReport ConversionReport, preErr error, invoke func() (A, ethguard.CallResult, error), convert func(A, ethguard.CallResult) (B, ConversionReport, error)) (B, error) {
	var zero B
	var started time.Time
	if c.observing() {
		started = time.Now()
	}
	if preErr == nil && (c == nil || c.core == nil) {
		preErr = conversionError("Client", "core", "nil execution owner", nil)
	}
	if preErr != nil {
		c.observe(ctx, method, started, ethguard.CallResult{}, preReport, nil, preErr)
		return zero, preErr
	}
	value, result, err := invoke()
	if err != nil {
		c.observe(ctx, method, started, result, preReport, err, nil)
		return zero, err
	}
	out, report, adapterErr := convert(value, result)
	preReport.merge(report)
	c.observe(ctx, method, started, result, preReport, nil, adapterErr)
	if adapterErr != nil {
		return zero, adapterErr
	}
	return out, nil
}

func identity[T any](value T, _ ethguard.CallResult) (T, ConversionReport, error) {
	return value, ConversionReport{}, nil
}

// RevertErrorData reads native revert bytes without inventing an RPC error code.
// A present empty hex value returns a non-nil empty slice and true.
func RevertErrorData(err error) ([]byte, bool) {
	var revert *ethguard.RevertError
	if !errors.As(err, &revert) || revert == nil {
		return nil, false
	}
	if len(revert.DataRaw) != 0 {
		var raw string
		if json.Unmarshal(revert.DataRaw, &raw) != nil {
			return nil, false
		}
		check := converter{kind: "RevertError"}
		decoded := check.bytes("data", raw, -1)
		if check.err != nil {
			return nil, false
		}
		return decoded, true
	}
	if revert.Data == nil {
		return nil, false
	}
	return append([]byte{}, revert.Data...), true
}
