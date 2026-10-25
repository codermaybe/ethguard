package rpcwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"sync/atomic"

	"github.com/codermaybe/ethguard/rpc"
)

// Package rpcwire is EthGuard's project-owned JSON-RPC 2.0 wire codec. It
// encodes requests, decodes and validates responses, and produces the
// project-owned protocol errors and capabilities. It exists so a transport
// Adapter can replace go-ethereum's rpc client without teaching the upper
// layers a third-party wire format.
//
// The contract implemented here is the EthGuard native HTTP contract
// (docs/design/http-transport-differential.md, "EthGuard 自有 HTTP
// contract"), which deliberately tightens several Geth v1.17.5 behaviors:
// responses must carry jsonrpc 2.0 and a matching request ID, result and
// error are mutually exclusive, and an error member that cannot form a valid
// JSON-RPC error object is a malformed response rather than a code-0
// protocol error.
//
// The package deliberately has no go-ethereum imports. The capability
// interfaces in the rpc domain package are satisfied structurally; the
// assertions live in the test files.

const Version = "2.0"

// Response-classification sentinels. They are transport-level failures, not
// JSON-RPC protocol errors: pool classifies them as failures and never as
// usable answers, matching the audited class A semantics for malformed and
// empty responses.
var (
	// ErrEmptyResponse reports a 2xx response with an empty (or
	// whitespace-only) body. It replaces Geth's raw io.EOF with an
	// EthGuard-owned classification.
	ErrEmptyResponse = errors.New("rpcwire: empty response body")
	// ErrMalformedResponse reports a body that is not a valid JSON-RPC 2.0
	// response object: invalid JSON, wrong version, result/error mutual
	// exclusion violations, a missing result, or an error member that
	// cannot form a valid JSON-RPC error object.
	ErrMalformedResponse = errors.New("rpcwire: malformed JSON-RPC response")
	// ErrIDMismatch reports a response whose id member does not match the
	// request id. This is a deliberate tightening over Geth, which accepted
	// responses by position without checking the id.
	ErrIDMismatch = errors.New("rpcwire: response id does not match request id")
)

func malformedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformedResponse, fmt.Sprintf(format, args...))
}

// IDGen produces request IDs. IDs are unique per IDGen, concurrency-safe,
// and serialize as JSON number literals. The sequence starts at 1 for
// diffability with the Geth baseline, but callers must not depend on the
// start value or monotonicity (EthGuard contract, request IDs).
type IDGen struct {
	counter atomic.Uint64
}

func NewIDGen() *IDGen { return &IDGen{} }

// Next returns the next request ID as raw JSON bytes.
func (g *IDGen) Next() json.RawMessage {
	n := g.counter.Add(1)
	return json.RawMessage(strconv.AppendUint(nil, n, 10))
}

// EncodeRequest builds one JSON-RPC 2.0 request object.
//
// params == nil omits the params member entirely; a non-nil empty slice
// sends "params":[] — the two caller states stay distinguishable on the
// wire (audited class A). Params values are marshaled with encoding/json;
// a value that cannot be marshaled fails the request before any bytes are
// sent.
func EncodeRequest(id json.RawMessage, method string, params []any) ([]byte, error) {
	if _, ok := parseID(id); !ok {
		return nil, errors.New("rpcwire: request id must be a JSON number or string")
	}
	if method == "" {
		return nil, errors.New("rpcwire: request method must not be empty")
	}
	var paramsJSON json.RawMessage
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("rpcwire: encode params: %w", err)
		}
		paramsJSON = raw
	}
	msg := struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{
		Version: Version,
		ID:      append(json.RawMessage(nil), id...),
		Method:  method,
		Params:  paramsJSON,
	}
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("rpcwire: encode request: %w", err)
	}
	return out, nil
}

// wireResponse mirrors the members DecodeResponse validates. json.RawMessage
// fields distinguish an absent member (nil) from a present JSON null (the
// bytes "null").
type wireResponse struct {
	Version json.RawMessage `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// Response is one validated JSON-RPC 2.0 response.
type Response struct {
	// ID is the raw bytes of the response id member (validated to match the
	// request ID).
	ID json.RawMessage
	// Result is the raw result bytes; a JSON null arrives as the bytes
	// "null", not as an error. Valid only when Error is nil.
	Result json.RawMessage
	// Error is the decoded provider error; nil when the response is a
	// success.
	Error *Error
}

// DecodeResponse parses one response body against the request ID and
// validates the EthGuard native contract. It returns either a Response
// (success or provider error) or an error wrapping one of the sentinels:
// ErrEmptyResponse, ErrMalformedResponse, or ErrIDMismatch. Validation
// failures never produce a protocol error or error-data presence.
func DecodeResponse(body []byte, requestID json.RawMessage) (*Response, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, ErrEmptyResponse
	}
	var wire wireResponse
	// json.Unmarshal (not a streaming decoder) rejects trailing garbage
	// after the top-level value and accepts only one complete value, which
	// is the one-request-one-response rule of the contract.
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, malformedf("body is not a JSON object: %v", err)
	}
	if string(wire.Version) != strconv.Quote(Version) {
		return nil, malformedf("unsupported or missing jsonrpc version %s", wire.Version)
	}
	if !idMatches(requestID, wire.ID) {
		return nil, fmt.Errorf("%w: request id %s, response id %s", ErrIDMismatch, requestID, wire.ID)
	}
	// Presence, not value: "result":null is present (success), while a
	// missing member is absent. "error":null is present and then rejected
	// because a valid error member must be an object.
	switch {
	case wire.Result != nil && wire.Error != nil:
		return nil, malformedf("result and error are mutually exclusive")
	case wire.Error != nil:
		rpcErr, err := decodeError(wire.Error)
		if err != nil {
			return nil, err
		}
		return &Response{ID: cloneRaw(wire.ID), Error: rpcErr}, nil
	case wire.Result != nil:
		return &Response{ID: cloneRaw(wire.ID), Result: cloneRaw(wire.Result)}, nil
	default:
		return nil, malformedf("response has neither result nor error")
	}
}

// Error is a project-owned JSON-RPC error object decoded from a response.
// It structurally satisfies rpc.ProtocolError (ErrorCode), rpc.DataError
// (ErrorData), and rpc.ErrorDataPresence (ErrorDataJSON) without any
// go-ethereum type in its chain.
type Error struct {
	code        int
	message     string
	data        json.RawMessage
	dataPresent bool
	decoded     any
}

func (e *Error) Error() string { return e.message }

// ErrorCode returns the provider's JSON-RPC error code.
func (e *Error) ErrorCode() int { return e.code }

// ErrorDataJSON returns the raw data bytes and whether the data member was
// present at all. present is false only for an absent member; a JSON null
// is present with the raw bytes "null" (audited class A three-state rule).
func (e *Error) ErrorDataJSON() (json.RawMessage, bool) {
	if e == nil || !e.dataPresent {
		return nil, false
	}
	return append(json.RawMessage(nil), e.data...), true
}

// ErrorData returns the decoded data value, or nil for an absent member and
// for a JSON null. Decoding follows encoding/json defaults (numbers become
// float64), mirroring the Geth baseline's decoded DataError view. Consumers
// that need the exact wire bytes must use ErrorDataJSON.
func (e *Error) ErrorData() any {
	if e == nil {
		return nil
	}
	return e.decoded
}

func decodeError(raw json.RawMessage) (*Error, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, malformedf("error member is not an object: %v", err)
	}
	codeRaw, ok := fields["code"]
	if !ok {
		return nil, malformedf("error member has no code")
	}
	// encoding/json would happily unmarshal the JSON string "3" into a
	// json.Number, so the raw literal must be checked to be a JSON number
	// before decoding: a quoted code is a malformed error member.
	if !isJSONNumberLiteral(codeRaw) {
		return nil, malformedf("error.code is not a number: %s", codeRaw)
	}
	var codeNum json.Number
	if err := json.Unmarshal(codeRaw, &codeNum); err != nil {
		return nil, malformedf("error.code is not a number: %s", codeRaw)
	}
	code, ok := integralNumber(codeNum)
	if !ok {
		return nil, malformedf("error.code is not an integer: %s", codeNum)
	}
	messageRaw, ok := fields["message"]
	if !ok {
		return nil, malformedf("error member has no message")
	}
	var message string
	if err := json.Unmarshal(messageRaw, &message); err != nil {
		return nil, malformedf("error.message is not a string: %s", messageRaw)
	}
	e := &Error{code: int(code), message: message}
	if data, present := fields["data"]; present {
		e.dataPresent = true
		e.data = append(json.RawMessage(nil), data...)
		// The raw bytes came from a validated JSON body, so this cannot
		// fail; on the impossible path the decoded view stays nil and the
		// raw view remains authoritative.
		_ = json.Unmarshal(e.data, &e.decoded)
	}
	return e, nil
}

// idMatches compares the response id against the request id as JSON values:
// numbers compare numerically, strings compare exactly, and cross-type or
// null/complex ids never match. Strictness is deliberate (EthGuard
// contract, response validation).
func idMatches(requestID, responseID json.RawMessage) bool {
	req, ok := parseID(requestID)
	if !ok {
		return false
	}
	resp, ok := parseID(responseID)
	if !ok {
		return false
	}
	switch req := req.(type) {
	case json.Number:
		respNum, ok := resp.(json.Number)
		if !ok {
			return false
		}
		return numberEqual(req, respNum)
	case string:
		respStr, ok := resp.(string)
		return ok && req == respStr
	}
	return false
}

// parseID decodes one id literal. Only JSON numbers and strings are valid
// request/response ids; null, booleans, objects, and arrays are not.
func parseID(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	switch v.(type) {
	case json.Number, string:
		return v, true
	}
	return nil, false
}

// numberEqual compares two JSON number literals numerically when both are
// integers (the only form this package generates), falling back to literal
// equality.
func numberEqual(a, b json.Number) bool {
	ai, aok := new(big.Int).SetString(a.String(), 10)
	bi, bok := new(big.Int).SetString(b.String(), 10)
	if aok && bok {
		return ai.Cmp(bi) == 0
	}
	return a.String() == b.String()
}

// isJSONNumberLiteral reports whether the raw bytes are a JSON number
// literal (optional minus sign followed by a digit). JSON forbids leading
// '+' and whitespace inside a value, so a first-byte check is exact here.
func isJSONNumberLiteral(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '-' {
		return len(raw) > 1 && raw[1] >= '0' && raw[1] <= '9'
	}
	return raw[0] >= '0' && raw[0] <= '9'
}

// integralNumber reports whether the literal is an integer and returns it.
func integralNumber(n json.Number) (int64, bool) {
	v, ok := new(big.Int).SetString(n.String(), 10)
	if !ok || !v.IsInt64() {
		return 0, false
	}
	return v.Int64(), true
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

// The decoded error satisfies the project-owned protocol-error capabilities
// structurally, so pool, aggregator and write consume only these interfaces
// no matter which Adapter produced the error.
var (
	_ rpc.ProtocolError     = (*Error)(nil)
	_ rpc.DataError         = (*Error)(nil)
	_ rpc.ErrorDataPresence = (*Error)(nil)
)
