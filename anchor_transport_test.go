package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"ethguard"
	"ethguard/config"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Anchor transport tests pin the production seam: HTTP and WSS are both dialed
// by pool.standardDialer through ethclient.DialContext. The resolver never
// inspects the URL scheme, and these tests do not add a second one.

type anchorTransport string

const (
	transportHTTP anchorTransport = "http"
	transportWS   anchorTransport = "ws"
)

type anchorHead struct {
	number string
	hash   string
	height uint64
}

var agreedHeads = map[string]anchorHead{
	"latest":    {number: "0x10", hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", height: 16},
	"safe":      {number: "0x20", hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", height: 32},
	"finalized": {number: "0x18", hash: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", height: 24},
}

type scriptCall struct {
	Method string
	Args   []any
}

type scriptFunc func(method string, args []any) (any, error)

var errScriptPass = errors.New("script pass")

type scriptRPCError struct {
	code int
	msg  string
}

func (e scriptRPCError) Error() string  { return e.msg }
func (e scriptRPCError) ErrorCode() int { return e.code }

type scriptNode struct {
	mu      sync.Mutex
	kind    anchorTransport
	chainID uint64
	handler scriptFunc
	calls   []scriptCall
	srv     *httptest.Server
}

type scriptAPI struct{ node *scriptNode }

func (a *scriptAPI) ChainId() (any, error) {
	return a.node.dispatch("eth_chainId", nil)
}

func (a *scriptAPI) GetBlockByNumber(number string, full bool) (any, error) {
	return a.node.dispatch("eth_getBlockByNumber", []any{number, full})
}

func (a *scriptAPI) GetBalance(address string, block string) (any, error) {
	return a.node.dispatch("eth_getBalance", []any{address, block})
}

func (a *scriptAPI) GetLogs(filter json.RawMessage) (any, error) {
	return a.node.dispatch("eth_getLogs", []any{filter})
}

func (a *scriptAPI) FeeHistory(count json.RawMessage, newest json.RawMessage, percentiles json.RawMessage) (any, error) {
	return a.node.dispatch("eth_feeHistory", []any{count, newest, percentiles})
}

func newScriptNode(t *testing.T, kind anchorTransport, chainID uint64) *scriptNode {
	t.Helper()
	node := &scriptNode{kind: kind, chainID: chainID}
	server := gethrpc.NewServer()
	if err := server.RegisterName("eth", &scriptAPI{node: node}); err != nil {
		t.Fatal(err)
	}
	var handler http.Handler = server
	if kind == transportWS {
		handler = server.WebsocketHandler([]string{"*"})
	}
	node.srv = httptest.NewServer(handler)
	t.Cleanup(server.Stop)
	t.Cleanup(node.srv.Close)
	return node
}

func (n *scriptNode) URL() string {
	if n.kind == transportWS {
		return "ws" + strings.TrimPrefix(n.srv.URL, "http")
	}
	return n.srv.URL
}

func (n *scriptNode) setHandler(fn scriptFunc) {
	n.mu.Lock()
	n.handler = fn
	n.mu.Unlock()
}

func (n *scriptNode) dispatch(method string, args []any) (any, error) {
	copied := append([]any(nil), args...)
	n.mu.Lock()
	n.calls = append(n.calls, scriptCall{Method: method, Args: copied})
	handler := n.handler
	chainID := n.chainID
	n.mu.Unlock()
	if handler != nil {
		result, err := handler(method, copied)
		if !errors.Is(err, errScriptPass) {
			return result, err
		}
	}
	if method == "eth_chainId" && chainID > 0 {
		return fmt.Sprintf("0x%x", chainID), nil
	}
	return nil, scriptRPCError{code: -32601, msg: "method not found: " + method}
}

func (n *scriptNode) snapshot() []scriptCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]scriptCall, len(n.calls))
	copy(out, n.calls)
	return out
}

func (n *scriptNode) reset() {
	n.mu.Lock()
	n.calls = nil
	n.mu.Unlock()
}

func newAnchorFleet(t *testing.T, kind anchorTransport, count int) (*ethguard.Client, []*scriptNode) {
	t.Helper()
	nodes := make([]*scriptNode, 0, count)
	cfg := config.Config{ChainID: 1}
	for index := range count {
		node := newScriptNode(t, kind, 1)
		nodes = append(nodes, node)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%d", index),
			Endpoint: node.URL(),
		})
	}
	client, err := ethguard.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, nodes
}

func newDeadFleet(t *testing.T, scheme string) *ethguard.Client {
	t.Helper()
	client, err := ethguard.New(config.Config{Upstreams: []config.Upstream{
		{ID: "node-0", Endpoint: scheme + "://127.0.0.1:1"},
		{ID: "node-1", Endpoint: scheme + "://127.0.0.1:2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func resetNodes(nodes []*scriptNode) {
	for _, node := range nodes {
		node.reset()
	}
}

func dumpCalls(nodes []*scriptNode) string {
	var buf strings.Builder
	for index, node := range nodes {
		fmt.Fprintf(&buf, "node %d %s\n", index, node.URL())
		for _, call := range node.snapshot() {
			fmt.Fprintf(&buf, "  %s %s\n", call.Method, mustJSON(call.Args))
		}
	}
	return buf.String()
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

func scriptSame(nodes []*scriptNode, fn scriptFunc) {
	for _, node := range nodes {
		node.setHandler(fn)
	}
}

func agreedHeadHandler(method string, args []any) (any, error) {
	switch method {
	case "eth_getBlockByNumber":
		tag, _ := args[0].(string)
		head, ok := agreedHeads[tag]
		if !ok {
			return nil, scriptRPCError{code: -32602, msg: "unexpected anchor tag " + tag}
		}
		return map[string]any{"number": head.number, "hash": head.hash}, nil
	case "eth_getBalance":
		return "0x64", nil
	case "eth_getLogs":
		return []any{}, nil
	case "eth_feeHistory":
		return map[string]any{
			"oldestBlock":   "0x1",
			"baseFeePerGas": []string{"0x1", "0x2"},
			"gasUsedRatio":  []float64{0.5},
		}, nil
	default:
		return nil, errScriptPass
	}
}

type referenceView struct {
	Status       rpc.Status
	Decision     rpc.Status
	Origin       string
	Tag          string
	Hash         string
	Height       uint64
	Observed     bool
	Faults       int
	Support      int
	Reject       int
	Participants int
	Class        string
	AnchorPrefix bool
}

func viewOf(decision rpc.Decision, err error) referenceView {
	view := referenceView{
		Decision:     decision.Status,
		Class:        anchorErrClass(err),
		AnchorPrefix: err != nil && strings.Contains(err.Error(), "anchor:"),
	}
	if decision.Reference == nil {
		return view
	}
	ref := decision.Reference
	view.Status = ref.Status
	view.Origin = ref.Origin
	view.Tag = ref.Tag
	view.Hash = ref.Hash
	view.Height = ref.Height
	view.Observed = !ref.ObservedAt.IsZero()
	view.Faults = len(ref.Faults)
	view.Support = len(ref.Support)
	view.Reject = len(ref.Reject)
	view.Participants = len(ref.Participants)
	return view
}

func anchorErrClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, rpc.ErrConflict):
		return "conflict"
	case errors.Is(err, rpc.ErrMalformedResponse):
		return "malformed"
	case errors.Is(err, rpc.ErrInvalidRequest):
		return "invalid"
	case errors.Is(err, ethguard.ErrAllSourcesDown):
		return "transport"
	case errors.Is(err, rpc.ErrNoValidResponse):
		return "no-valid"
	default:
		return "other"
	}
}

func assertEach(t *testing.T, nodes []*scriptNode, method string, want int) {
	t.Helper()
	for index, node := range nodes {
		got := 0
		for _, call := range node.snapshot() {
			if call.Method == method {
				got++
			}
		}
		if got != want {
			t.Fatalf("node %d %s calls = %d, want %d\n%s", index, method, got, want, dumpCalls(nodes))
		}
	}
}

func assertNoBusiness(t *testing.T, nodes []*scriptNode) {
	t.Helper()
	for _, node := range nodes {
		for _, call := range node.snapshot() {
			switch call.Method {
			case "eth_chainId", "eth_getBlockByNumber":
			default:
				t.Fatalf("business call %s after anchor failure\n%s", call.Method, dumpCalls(nodes))
			}
		}
	}
}

func assertBlockTag(t *testing.T, nodes []*scriptNode, tag string) {
	t.Helper()
	assertEach(t, nodes, "eth_getBlockByNumber", 1)
	for _, node := range nodes {
		for _, call := range node.snapshot() {
			if call.Method != "eth_getBlockByNumber" {
				continue
			}
			got, _ := call.Args[0].(string)
			if got != tag {
				t.Fatalf("anchor tag = %q, want %q (downgraded?)\n%s", got, tag, dumpCalls(nodes))
			}
		}
	}
}

func jsonString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.RawMessage:
		var decoded string
		if err := json.Unmarshal(typed, &decoded); err != nil {
			return "", false
		}
		return decoded, true
	default:
		return "", false
	}
}

func filterField(value any, key string) (string, bool) {
	raw, ok := value.(json.RawMessage)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", false
		}
		raw = encoded
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", false
	}
	field, ok := object[key].(string)
	return field, ok
}

func callMethod(ctx context.Context, client *ethguard.Client, method string, params ...any) (rpc.Decision, error) {
	return client.Call(ctx, method, params...)
}

// TestAnchorRequestHashIsLowercase pins the request spelling separately from
// the returned value. When the winning head spells its hash with a 0X prefix,
// the hash sent to sources afterwards is lowercase, while the block decision's
// Value keeps the source's own 0X spelling. Folding only the comparison key,
// or lowercasing the Value as well, fails this test.
func TestAnchorRequestHashIsLowercase(t *testing.T) {
	const (
		number = "0x10"
		upper  = "0Xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		lower  = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	client, nodes := newAnchorFleet(t, transportHTTP, 2)
	scriptSame(nodes, func(method string, args []any) (any, error) {
		switch method {
		case "eth_getBlockByNumber":
			return map[string]any{"number": number, "hash": upper}, nil
		case "eth_getBalance":
			return "0x64", nil
		default:
			return nil, errScriptPass
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	decision, err := callMethod(ctx, client, methods.MethodGetBalance, "0xaddr", "latest")
	if err != nil {
		t.Fatalf("getBalance: %v\n%s", err, dumpCalls(nodes))
	}
	if decision.Reference == nil || decision.Reference.Hash != lower {
		t.Fatalf("reference hash = %+v, want %s", decision.Reference, lower)
	}
	for _, node := range nodes {
		for _, call := range node.snapshot() {
			if call.Method != methods.MethodGetBalance {
				continue
			}
			block, _ := call.Args[1].(string)
			if block != lower {
				t.Fatalf("balance block = %q, want lowercase %s", block, lower)
			}
			if block == upper {
				t.Fatalf("balance block kept the source spelling %s", upper)
			}
		}
	}

	block, err := callMethod(ctx, client, methods.MethodGetBlockByNumber, "latest", false)
	if err != nil {
		t.Fatalf("getBlockByNumber: %v\n%s", err, dumpCalls(nodes))
	}
	if !strings.Contains(string(block.Value), upper) {
		t.Fatalf("block value = %s, want the source spelling %s", block.Value, upper)
	}
	if strings.Contains(string(block.Value), lower) {
		t.Fatalf("block value was lowercased: %s", block.Value)
	}
}

func TestAnchorTransportReferenceMatchesAcrossHTTPAndWSS(t *testing.T) {
	kinds := []anchorTransport{transportHTTP, transportWS}
	clients := make(map[anchorTransport]*ethguard.Client, len(kinds))
	fleets := make(map[anchorTransport][]*scriptNode, len(kinds))
	for _, kind := range kinds {
		client, nodes := newAnchorFleet(t, kind, 2)
		scriptSame(nodes, agreedHeadHandler)
		clients[kind] = client
		fleets[kind] = nodes
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, tag := range []string{"latest", "safe", "finalized"} {
		head := agreedHeads[tag]
		heightHex := "0x" + strconv.FormatUint(head.height, 16)
		views := make(map[anchorTransport]map[string]referenceView, len(kinds))
		for _, kind := range kinds {
			views[kind] = exerciseTag(t, ctx, clients[kind], fleets[kind], tag, head, heightHex)
		}
		if fmt.Sprint(views[transportHTTP]) != fmt.Sprint(views[transportWS]) {
			t.Fatalf("%s evidence diverged\nhttp %#v\nws %#v", tag, views[transportHTTP], views[transportWS])
		}
	}
}

func exerciseTag(t *testing.T, ctx context.Context, client *ethguard.Client, nodes []*scriptNode, tag string, head anchorHead, heightHex string) map[string]referenceView {
	t.Helper()
	views := make(map[string]referenceView)

	resetNodes(nodes)
	gotHash, err := client.AnchorBlockHashForTag(ctx, tag)
	if err != nil {
		t.Fatalf("AnchorBlockHashForTag(%s): %v\n%s", tag, err, dumpCalls(nodes))
	}
	if gotHash != head.hash {
		t.Fatalf("AnchorBlockHashForTag(%s) = %s, want %s", tag, gotHash, head.hash)
	}
	assertBlockTag(t, nodes, tag)

	resetNodes(nodes)
	decision, err := callMethod(ctx, client, methods.MethodGetBalance, "0xaddr", tag)
	if err != nil {
		t.Fatalf("getBalance %s: %v\n%s", tag, err, dumpCalls(nodes))
	}
	assertBlockTag(t, nodes, tag)
	assertEach(t, nodes, methods.MethodGetBalance, 1)
	for _, node := range nodes {
		for _, call := range node.snapshot() {
			if call.Method != methods.MethodGetBalance {
				continue
			}
			block, _ := call.Args[1].(string)
			if block != head.hash {
				t.Fatalf("balance block = %q, want hash %s\n%s", block, head.hash, dumpCalls(nodes))
			}
		}
	}
	if decision.Reference == nil || decision.Reference.Hash != head.hash || decision.Reference.Height != head.height {
		t.Fatalf("balance reference = %+v, want hash %s height %d from one establishment", decision.Reference, head.hash, head.height)
	}
	views["balance"] = viewOf(decision, nil)

	resetNodes(nodes)
	decision, err = callMethod(ctx, client, methods.MethodGetLogs, map[string]any{"fromBlock": tag, "toBlock": tag})
	if err != nil {
		t.Fatalf("getLogs %s: %v\n%s", tag, err, dumpCalls(nodes))
	}
	assertBlockTag(t, nodes, tag)
	assertEach(t, nodes, methods.MethodGetLogs, 1)
	for _, node := range nodes {
		for _, call := range node.snapshot() {
			if call.Method != methods.MethodGetLogs {
				continue
			}
			fromBlock, okFrom := filterField(call.Args[0], "fromBlock")
			toBlock, okTo := filterField(call.Args[0], "toBlock")
			if !okFrom || !okTo || fromBlock != heightHex || toBlock != heightHex {
				t.Fatalf("logs bounds = %q %q, want %s\n%s", fromBlock, toBlock, heightHex, dumpCalls(nodes))
			}
		}
	}
	if decision.Reference == nil || decision.Reference.Hash != head.hash || decision.Reference.Height != head.height {
		t.Fatalf("logs reference = %+v, want the same establishment as the hash anchor", decision.Reference)
	}
	views["logs"] = viewOf(decision, nil)

	resetNodes(nodes)
	decision, err = callMethod(ctx, client, methods.MethodFeeHistory, "0x1", tag, []float64{})
	if err != nil {
		t.Fatalf("feeHistory %s: %v\n%s", tag, err, dumpCalls(nodes))
	}
	assertBlockTag(t, nodes, tag)
	assertEach(t, nodes, methods.MethodFeeHistory, 1)
	for _, node := range nodes {
		for _, call := range node.snapshot() {
			if call.Method != methods.MethodFeeHistory {
				continue
			}
			newest, ok := jsonString(call.Args[1])
			if !ok || newest != heightHex {
				t.Fatalf("feeHistory newest = %q, want %s\n%s", newest, heightHex, dumpCalls(nodes))
			}
		}
	}
	if decision.Reference == nil || decision.Reference.Hash != head.hash || decision.Reference.Height != head.height {
		t.Fatalf("feeHistory reference = %+v, want the same establishment as the hash anchor", decision.Reference)
	}
	views["fee"] = viewOf(decision, nil)
	return views
}

func TestAnchorTransportFailuresMatchAcrossHTTPAndWSS(t *testing.T) {
	t.Run("dial", func(t *testing.T) {
		httpView := failBalance(t, newDeadFleet(t, "http"), "latest")
		wsView := failBalance(t, newDeadFleet(t, "ws"), "latest")
		if httpView != wsView {
			t.Fatalf("dial failure diverged\nhttp %#v\nws %#v", httpView, wsView)
		}
		if httpView.Class != "transport" || httpView.Decision != rpc.Unknown || httpView.Status != rpc.Unknown || !httpView.Observed || !httpView.AnchorPrefix || httpView.Faults == 0 {
			t.Fatalf("dial failure view = %#v", httpView)
		}
	})

	t.Run("json-rpc", func(t *testing.T) {
		assertScriptedFailure(t, "no-valid", "head unavailable", func(method string, args []any) (any, error) {
			if method == "eth_getBlockByNumber" {
				return nil, scriptRPCError{code: -32000, msg: "head unavailable"}
			}
			return nil, errScriptPass
		})
	})

	t.Run("malformed-head", func(t *testing.T) {
		// An empty head fails normalization. The shared class is
		// ErrNoValidResponse, not ErrMalformedResponse.
		assertScriptedFailure(t, "no-valid", "missing number or hash", func(method string, args []any) (any, error) {
			if method == "eth_getBlockByNumber" {
				return map[string]any{}, nil
			}
			return nil, errScriptPass
		})
	})

	t.Run("conflict", func(t *testing.T) {
		compareScripted(t, "conflict", "", func(index int) scriptFunc {
			return func(method string, args []any) (any, error) {
				if method != "eth_getBlockByNumber" {
					return nil, errScriptPass
				}
				return map[string]any{
					"number": fmt.Sprintf("0x%x", 16+index),
					"hash":   fmt.Sprintf("0x%064x", index+1),
				}, nil
			}
		})
	})

	t.Run("canceled", func(t *testing.T) {
		compareScripted(t, "canceled", "", func(int) scriptFunc { return agreedHeadHandler })
	})
}

func assertScriptedFailure(t *testing.T, class string, faultText string, fn scriptFunc) {
	t.Helper()
	compareScripted(t, class, faultText, func(int) scriptFunc { return fn })
}

func compareScripted(t *testing.T, class string, faultText string, handlerFor func(int) scriptFunc) {
	t.Helper()
	views := map[anchorTransport]referenceView{}
	for _, kind := range []anchorTransport{transportHTTP, transportWS} {
		client, nodes := newAnchorFleet(t, kind, 2)
		for index, node := range nodes {
			node.setHandler(handlerFor(index))
		}
		var decision rpc.Decision
		var err error
		if class == "canceled" {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			decision, err = client.Call(ctx, methods.MethodGetBalance, "0xaddr", "safe")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			decision, err = client.Call(ctx, methods.MethodGetBalance, "0xaddr", "safe")
			cancel()
		}
		if err == nil {
			t.Fatalf("%s %s succeeded\n%s", kind, class, dumpCalls(nodes))
		}
		assertNoBusiness(t, nodes)
		if class != "canceled" {
			assertEach(t, nodes, "eth_getBlockByNumber", 1)
			assertBlockTag(t, nodes, "safe")
		}
		view := viewOf(decision, err)
		if faultText != "" && !faultContains(decision, faultText) {
			t.Fatalf("%s faults missing %q: %+v", kind, faultText, decision.Reference)
		}
		views[kind] = view
	}
	if views[transportHTTP] != views[transportWS] {
		t.Fatalf("%s failure diverged\nhttp %#v\nws %#v", class, views[transportHTTP], views[transportWS])
	}
	got := views[transportHTTP]
	if got.Class != class || got.Decision != rpc.Unknown || got.Status != rpc.Unknown || !got.Observed || !got.AnchorPrefix {
		t.Fatalf("%s view = %#v", class, got)
	}
}

func faultContains(decision rpc.Decision, text string) bool {
	if decision.Reference == nil {
		return false
	}
	for _, fault := range decision.Reference.Faults {
		if fault != nil && strings.Contains(fault.Error(), text) {
			return true
		}
	}
	return false
}

func failBalance(t *testing.T, client *ethguard.Client, tag string) referenceView {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", tag)
	if err == nil {
		t.Fatal("expected anchor failure")
	}
	return viewOf(decision, err)
}

func TestAnchorTransportReestablishesEachCall(t *testing.T) {
	for _, kind := range []anchorTransport{transportHTTP, transportWS} {
		t.Run(string(kind), func(t *testing.T) {
			client, nodes := newAnchorFleet(t, kind, 2)
			scriptSame(nodes, agreedHeadHandler)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			first, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
			if err != nil {
				t.Fatal(err)
			}
			second, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
			if err != nil {
				t.Fatal(err)
			}
			if first.Reference == nil || second.Reference == nil {
				t.Fatalf("missing reference first=%+v second=%+v", first.Reference, second.Reference)
			}
			if first.Reference.Hash != agreedHeads["latest"].hash || first.Reference.Height != agreedHeads["latest"].height {
				t.Fatalf("first reference = %+v", first.Reference)
			}
			if second.Reference.Hash != first.Reference.Hash || second.Reference.Height != first.Reference.Height {
				t.Fatalf("second reference changed head: %+v", second.Reference)
			}
			if second.Reference.ObservedAt.Equal(first.Reference.ObservedAt) {
				t.Fatalf("second call reused ObservedAt %s", first.Reference.ObservedAt)
			}
			assertEach(t, nodes, "eth_getBlockByNumber", 2)
			assertEach(t, nodes, "eth_chainId", 1)
			for _, node := range nodes {
				var tags []string
				for _, call := range node.snapshot() {
					if call.Method == "eth_getBlockByNumber" {
						tag, _ := call.Args[0].(string)
						tags = append(tags, tag)
					}
				}
				if len(tags) != 2 || tags[0] != "latest" || tags[1] != "latest" {
					t.Fatalf("block tags = %v, want two latest establishments", tags)
				}
			}
		})
	}
}
