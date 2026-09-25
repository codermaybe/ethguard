// Package rpctest provides in-process Ethereum JSON-RPC nodes for testing.
//
// A Node is a real HTTP JSON-RPC server, so pools and clients dial it through
// their production path; no network access or chain client is required. Each
// method is scripted independently, which makes multi-source scenarios —
// agreement, dissent, hangs, broken providers — reproducible in unit tests.
package rpctest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Call is one recorded JSON-RPC request received by a Node.
type Call struct {
	Method string
	Params []json.RawMessage
	// Context is the request's server-side context. It is canceled when the
	// caller gives up (deadline, cancellation, connection loss), so scripted
	// handlers can abandon stalled answers instead of blocking Node.Close.
	Context context.Context
}

// Handler returns the result for one call. A non-nil error is delivered to the
// RPC caller as a JSON-RPC error object; use RPCError to control the code.
type Handler func(Call) (any, error)

// Value returns a handler that always answers with the same result.
func Value(result any) Handler {
	return func(Call) (any, error) { return result, nil }
}

// After wraps a handler with a fixed delay, simulating latency.
func After(delay time.Duration, next Handler) Handler {
	return func(c Call) (any, error) {
		time.Sleep(delay)
		return next(c)
	}
}

type rpcError struct {
	code        int
	message     string
	data        json.RawMessage
	dataPresent bool
}

func (e rpcError) Error() string { return e.message }

// RPCError builds a handler error that carries an explicit JSON-RPC code.
// The encoded error object has no data field.
func RPCError(code int, message string) error {
	return rpcError{code: code, message: message}
}

// RPCErrorWithData builds a handler error whose data field is under the
// caller's control. present is false when the field must be omitted, including
// the distinct case of a JSON null, which is present with data "null".
func RPCErrorWithData(code int, message string, data json.RawMessage, present bool) error {
	return rpcError{
		code:        code,
		message:     message,
		data:        append(json.RawMessage(nil), data...),
		dataPresent: present,
	}
}

// httpStatusError is an HTTP failure with no JSON-RPC body. Empty 429 and
// 403 responses from captured samples are transport faults, not protocol
// errors, and must not be rewritten into a JSON-RPC error object.
type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("http %d", e.status) }

// Node is one scriptable Ethereum JSON-RPC server.
type Node struct {
	mu       sync.Mutex
	chainID  uint64
	handlers map[string]Handler
	calls    []Call
	srv      *httptest.Server
}

// NewNode starts serving immediately. The node answers eth_chainId with the
// given chain ID (as pools verify identity on first use); pass zero to answer
// with no default identity.
func NewNode(chainID uint64) *Node {
	n := &Node{
		chainID:  chainID,
		handlers: make(map[string]Handler),
	}
	n.srv = httptest.NewServer(http.HandlerFunc(n.serve))
	return n
}

// URL is the dial target for this node.
func (n *Node) URL() string { return n.srv.URL }

// Close shuts the server down.
func (n *Node) Close() { n.srv.Close() }

// Handle installs or replaces the handler for one method.
func (n *Node) Handle(method string, h Handler) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.handlers[method] = h
}

// Return scripts one method to always answer with the same result.
func (n *Node) Return(method string, result any) {
	n.Handle(method, Value(result))
}

// Fail scripts one method to always answer with a JSON-RPC error that has
// no data field.
func (n *Node) Fail(method string, code int, message string) {
	n.Handle(method, func(Call) (any, error) { return nil, RPCError(code, message) })
}

// FailWithData scripts one method to answer with a JSON-RPC error whose data
// presence is exact. present false omits the field. present true writes data
// unchanged, including JSON null, "0x", and concrete values.
func (n *Node) FailWithData(method string, code int, message string, data json.RawMessage, present bool) {
	err := RPCErrorWithData(code, message, data, present)
	n.Handle(method, func(Call) (any, error) { return nil, err })
}

// ReturnRaw scripts one method to answer with result bytes unchanged.
func (n *Node) ReturnRaw(method string, result json.RawMessage) {
	copied := append(json.RawMessage(nil), result...)
	n.Handle(method, func(Call) (any, error) { return copied, nil })
}

// FailHTTP scripts one method to answer with an HTTP status and an empty
// body. Callers observe a transport failure rather than a JSON-RPC error.
func (n *Node) FailHTTP(method string, status int) {
	n.Handle(method, func(Call) (any, error) { return nil, &httpStatusError{status: status} })
}

// Hang scripts one method to delay before answering, simulating a stalled
// provider. Callers bound by context deadline observe a timeout; the handler
// abandons the answer as soon as the caller's request goes away.
func (n *Node) Hang(method string, delay time.Duration) {
	n.Handle(method, func(c Call) (any, error) {
		select {
		case <-time.After(delay):
			return nil, nil
		case <-c.Context.Done():
			return nil, c.Context.Err()
		}
	})
}

// Calls returns a snapshot of every request the node has received.
func (n *Node) Calls() []Call {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Call, len(n.calls))
	copy(out, n.calls)
	return out
}

func (n *Node) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("rpctest: bad request: %v", err), http.StatusBadRequest)
		return
	}
	call := Call{Method: req.Method, Params: req.Params, Context: r.Context()}

	n.mu.Lock()
	n.calls = append(n.calls, call)
	handler := n.handlers[req.Method]
	if handler == nil {
		handler = n.defaultHandler()
	}
	n.mu.Unlock()

	result, err := handler(call)

	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		w.WriteHeader(httpErr.status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error":   errorObject(err),
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  result,
	})
}

func errorObject(err error) map[string]any {
	object := map[string]any{"code": -32000, "message": err.Error()}
	var re rpcError
	if !errors.As(err, &re) {
		return object
	}
	object["code"] = re.code
	object["message"] = re.message
	if !re.dataPresent {
		return object
	}
	data := re.data
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	object["data"] = json.RawMessage(append([]byte(nil), data...))
	return object
}

// defaultHandler answers eth_chainId when scripted behaviour is absent and
// rejects everything else as unimplemented.
func (n *Node) defaultHandler() Handler {
	return func(c Call) (any, error) {
		if c.Method == "eth_chainId" && n.chainID > 0 {
			return fmt.Sprintf("0x%x", n.chainID), nil
		}
		return nil, RPCError(-32601, "rpctest: method not found: "+c.Method)
	}
}
