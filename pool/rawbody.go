package pool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// geth decodes a JSON-RPC error's data into interface{}. A missing field and
// a JSON null therefore arrive as the same nil, and observationRPCError can
// no longer tell them apart. HTTP is the transport rpctest and recorded
// samples use, so the production dialer keeps the raw response body for the
// call that is in flight and lets collectOne restore presence before the
// error is classified. WebSocket frames stay on geth's codec; they do not
// pass through this body.
type rawBodyBox struct {
	body []byte
	ok   bool
}

type rawBodyKey struct{}

func withRawBody(ctx context.Context) (context.Context, *rawBodyBox) {
	box := &rawBodyBox{}
	return context.WithValue(ctx, rawBodyKey{}, box), box
}

func (b *rawBodyBox) bytes() ([]byte, bool) {
	if b == nil || !b.ok {
		return nil, false
	}
	return b.body, true
}

type capturingTransport struct {
	base http.RoundTripper
}

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
	raw, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	box.body = append([]byte(nil), raw...)
	box.ok = true
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

// dialHTTPPreservingErrorData dials HTTP and HTTPS with the capturing
// transport. Other schemes, including WebSocket, return ok=false so the
// caller keeps the ordinary ethclient dialer.
func dialHTTPPreservingErrorData(ctx context.Context, rawURL string) (*ethclient.Client, bool, error) {
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
		Transport: capturingTransport{},
	}))
	if err != nil {
		return nil, true, err
	}
	return ethclient.NewClient(client), true, nil
}

// preservedDataError carries the raw JSON of error.data without implementing
// geth's DataError. ethclient.RevertErrorData uses errors.As to find that
// interface and must keep landing on geth's own error.
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
