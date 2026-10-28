package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/codermaybe/ethguard/internal/limits"
	"net/http"
	"net/url"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard/rpc"
)

// ErrBodyTooLarge classifies a captured response body that exceeded the
// capture byte-count limit. An oversized body is never handed to geth as if
// it were a complete JSON-RPC response: the transport fails the call with
// this error instead, so the error-data preservation path never parses bytes
// that may be truncated.
var ErrBodyTooLarge = errors.New("pool: captured response body exceeds limit")

// errRawBodySlotTaken reports a second capture write into one slot. See
// rawBodyBox.set for why the invariant is enforced rather than assumed.
var errRawBodySlotTaken = errors.New("pool: raw response body already captured for this call context")

// defaultCapturedBodyBytes bounds how many bytes the capture path reads from
// a single response body. It is a byte-count bound, not a time bound: it
// cannot stop a body that dribbles its bytes slowly, which is what a deadline
// on the call context is for (see the precedence note in
// capturingTransport.RoundTrip).
const defaultCapturedBodyBytes = limits.MaxBodyBytes

// geth decodes a JSON-RPC error's data into interface{}. A missing field and
// a JSON null therefore arrive as the same nil, and observationRPCError can
// no longer tell them apart. HTTP is the transport rpctest and recorded
// samples use, so the HTTP compatibility Adapter keeps the raw response body
// for the call that is in flight and restores presence before returning the
// error to pool. WebSocket frames stay on geth's codec; they do not pass
// through this body.
type rawBodyBox struct {
	body []byte
	ok   bool
}

type rawBodyKey struct{}

// withRawBody binds a fresh single-slot box to one call context. The box is
// written at most once, by the capturing transport of the one in-flight
// RoundTrip this context carries. Single call context -> single in-flight
// RoundTrip -> at most one captured body is an explicit contract between
// collectOne and the transport, not an implicit assumption: set enforces it.
func withRawBody(ctx context.Context) (context.Context, *rawBodyBox) {
	box := &rawBodyBox{}
	return context.WithValue(ctx, rawBodyKey{}, box), box
}

// set installs the first captured body. A second write is refused with
// errRawBodySlotTaken and the first capture is kept: overwriting would
// silently attribute a later response (a redirect follow, a re-used call
// context, a concurrent RoundTrip) to this call, and the one-slot invariant
// must fail loudly instead of holding by luck.
func (b *rawBodyBox) set(body []byte) error {
	if b.ok {
		return errRawBodySlotTaken
	}
	b.body = append([]byte(nil), body...)
	b.ok = true
	return nil
}

func (b *rawBodyBox) bytes() ([]byte, bool) {
	if b == nil || !b.ok {
		return nil, false
	}
	return b.body, true
}

type capturingTransport struct {
	base http.RoundTripper

	// limit is the byte-count cap for one captured response body; the zero
	// value selects defaultCapturedBodyBytes. It bounds bytes read, not
	// elapsed time; see the read-precedence comment in RoundTrip.
	limit int64
}

func (t capturingTransport) bodyLimit() int64 {
	if t.limit > 0 {
		return t.limit
	}
	return defaultCapturedBodyBytes
}

// RoundTrip reads the response body only to preserve error.data presence and
// then serves geth the captured bytes verbatim.
//
// Error precedence when reading goes badly; each layer strictly dominates the
// next and is checked in this order:
//
//  1. Call-context cancellation or deadline: if req.Context() has fired by
//     the time the outcome is known, everything observed on the wire is
//     discarded and the context error is returned. A caller sees
//     context.Canceled / context.DeadlineExceeded even when the body was
//     simultaneously oversized and the connection broke mid-read.
//  2. Byte-count overrun: more than bodyLimit() bytes arrived. The captured
//     prefix is discarded and an error wrapping ErrBodyTooLarge is returned.
//     A truncated prefix must never reach geth, where it could parse as a
//     complete JSON-RPC error object and let preserveErrorData vouch for
//     data it truncated.
//  3. Connection failure: the read itself failed without an overrun (peer
//     reset, unexpected EOF, broken write side). The read error is returned
//     to the caller; the box stays empty.
//
// The cap bounds data volume only. Bounding wall-clock time is the
// caller's job: dialHTTPPreservingErrorData never injects a deadline, so a
// caller that needs one must place it on the context handed to
// collectOne (the pool-level call context already flows through).
func (t capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	box, _ := req.Context().Value(rawBodyKey{}).(*rawBodyBox)
	if box == nil {
		return resp, nil
	}
	// Reading limit+1 bytes separates an exact-limit body from an oversized
	// one without reading any further, so the rest of an oversized body is
	// never pulled in, only its first overrun byte.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, t.bodyLimit()+1))
	// The read already captured the outcome; a close error here carries no
	// additional decision, so it is dropped deliberately.
	_ = resp.Body.Close()
	if cerr := req.Context().Err(); cerr != nil {
		return nil, cerr
	}
	if int64(len(raw)) > t.bodyLimit() {
		return nil, fmt.Errorf("pool: captured %d bytes of response body, limit is %d: %w",
			len(raw), t.bodyLimit(), ErrBodyTooLarge)
	}
	if readErr != nil {
		return nil, readErr
	}
	if err := box.set(raw); err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

// dialHTTPPreservingErrorData dials HTTP and HTTPS with the capturing
// transport. Other schemes, including WebSocket, return ok=false so the
// caller keeps the ordinary ethclient dialer. It injects no deadline of its
// own: the call context handed in by the caller is the only time bound.
func dialHTTPPreservingErrorData(ctx context.Context, rawURL string) (*ethclient.Client, bool, error) {
	return dialHTTPPreservingErrorDataWithLimit(ctx, rawURL, 0)
}

// dialHTTPPreservingErrorDataWithLimit is dialHTTPPreservingErrorData with an
// explicit capture byte-count limit; limit 0 selects defaultCapturedBodyBytes.
// Tests use it to shrink the cap and exercise the truncation path without
// building multi-megabyte bodies.
func dialHTTPPreservingErrorDataWithLimit(ctx context.Context, rawURL string, limit int64) (*ethclient.Client, bool, error) {
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return nil, false, nil
	}
	switch endpoint.Scheme {
	case "http", "https":
	default:
		return nil, false, nil
	}
	client, err := gethrpc.DialOptions(ctx, rawURL, gethrpc.WithHTTPClient(&http.Client{
		Transport: capturingTransport{limit: limit},
	}))
	if err != nil {
		return nil, true, err
	}
	return ethclient.NewClient(client), true, nil
}

// preservedDataError carries the raw JSON of error.data without implementing
// geth's DataError. ethclient.RevertErrorData uses errors.As to find that
// interface and must keep landing on geth's own error. It satisfies the
// project-owned code and presence capabilities instead:
var (
	_ rpc.ProtocolError     = (*preservedDataError)(nil)
	_ rpc.ErrorDataPresence = (*preservedDataError)(nil)
)

type preservedDataError struct {
	err     error
	data    json.RawMessage
	present bool
}

func (e *preservedDataError) Error() string { return e.err.Error() }

func (e *preservedDataError) Unwrap() error { return e.err }

func (e *preservedDataError) ErrorCode() int {
	var coded gethrpc.Error
	if errors.As(e.err, &coded) {
		return coded.ErrorCode()
	}
	return 0
}

// ErrorDataJSON reports whether the JSON error object contained data.
// present is false when the field was absent. A present null is the raw
// bytes "null", not a nil slice.
func (e *preservedDataError) ErrorDataJSON() (json.RawMessage, bool) {
	if e == nil || !e.present {
		return nil, false
	}
	return append(json.RawMessage(nil), e.data...), true
}

// preserveErrorData rebuilds the error with the raw data-presence it had on
// the wire, using the body captured by the transport for this call.
//
// Body-attribution assumption (undeclared, same-origin): body holds the body
// of the LAST RoundTrip executed on the call context. In redirect chains or
// any scenario producing more than one response per call, these bytes belong
// to whichever response completed last, and preserveErrorData vouches for
// error.data as if it came from the response geth actually parsed. Single-hop
// upstreams (the only supported topology) keep the assumption trivially
// true; if a future change ever lets one call observe several responses,
// EITHER a per-response attribution seam must be added OR this function must
// be given the authoritative body. If a real mismatch bug is found, log it as
// its own defect first; do not silently change behavior here.
func preserveErrorData(err error, body []byte) error {
	if err == nil || len(body) == 0 {
		return err
	}
	var protocol gethrpc.Error
	if !errors.As(err, &protocol) {
		return err
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Error) == 0 || string(envelope.Error) == "null" {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope.Error, &fields) != nil {
		return err
	}
	data, present := fields["data"]
	if present {
		data = append(json.RawMessage(nil), data...)
	}
	return &preservedDataError{err: err, data: data, present: present}
}
