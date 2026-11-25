package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum/core/types"
)

// Independently pinned from execution-apis 981aa66's get-latest.io and the
// first simulated block's parent in ethSimulate-simple.io.
const simulatePin = "0x52bccfc9c4d4874b1898c0fbd0ae3b598a0edc7b94d9b796857dbbe1093c32e0"
const simulatePinNumber = "0x3c"

func assertSimulateReferenceFixture(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("../../spec/vectors/eth_getBlockByNumber/get-latest.io")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "<< ") {
			continue
		}
		var response struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "<< ")), &response); err != nil {
			t.Fatal(err)
		}
		var identity struct{ Hash, Number string }
		if err := json.Unmarshal(response.Result, &identity); err != nil {
			t.Fatal(err)
		}
		if identity.Hash != simulatePin || identity.Number != simulatePinNumber {
			t.Fatalf("official latest identity = %+v, want %s at %s", identity, simulatePin, simulatePinNumber)
		}
		var header types.Header
		if err := json.Unmarshal(response.Result, &header); err != nil {
			t.Fatal(err)
		}
		if header.Hash().Hex() != simulatePin || header.Number.Uint64() != 60 {
			t.Fatalf("official latest header commitment = %s at %s, want %s at 60", header.Hash(), header.Number, simulatePin)
		}
		return
	}
	t.Fatal("official latest fixture has no response")
}

type simulateVector struct {
	payload json.RawMessage
	request ethapi.SimulateV1Request
	base    string
	result  json.RawMessage
	blocks  []ethapi.SimulateBlock
}

func readSimulateVector(t *testing.T, name string) simulateVector {
	t.Helper()
	data, err := os.ReadFile("../../spec/vectors/eth_simulateV1/" + name)
	if err != nil {
		t.Fatalf("read execution-apis vector %s: %v", name, err)
	}
	var requestLine, responseLine string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, ">> ") {
			requestLine = strings.TrimPrefix(line, ">> ")
		}
		if strings.HasPrefix(line, "<< ") {
			responseLine = strings.TrimPrefix(line, "<< ")
		}
	}
	if requestLine == "" || responseLine == "" {
		t.Fatalf("execution-apis vector %s is missing a request or response", name)
	}
	var request struct {
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(requestLine), &request); err != nil {
		t.Fatalf("decode vector request %s: %v", name, err)
	}
	if len(request.Params) != 2 {
		t.Fatalf("vector request params = %d, want payload and base", len(request.Params))
	}
	var base string
	if err := json.Unmarshal(request.Params[1], &base); err != nil {
		t.Fatalf("decode vector base: %v", err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(responseLine), &response); err != nil {
		t.Fatalf("decode vector response %s: %v", name, err)
	}
	if len(response.Result) == 0 {
		t.Fatalf("vector %s has no result", name)
	}
	blocks, err := ethapi.NewSimulateV1Result(response.Result)
	if err != nil {
		t.Fatalf("parse vector result %s: %v", name, err)
	}
	payload, err := ethapi.NewSimulateV1Request(request.Params[0])
	if err != nil {
		t.Fatal(err)
	}
	return simulateVector{payload: request.Params[0], request: payload, base: base, result: response.Result, blocks: blocks}
}

type simulatePeer struct {
	peer             *sourceWirePeer
	request          atomic.Value
	simulateRequests atomic.Int32
}

func newSimulateFleet(t *testing.T, responses []json.RawMessage) (*ethguard.Client, []*simulatePeer) {
	return newSimulateFleetWithOverride(t, responses, nil)
}

func newSimulateFleetWithOverride(t *testing.T, responses []json.RawMessage, override func(int, sourceWireRequest) (string, bool)) (*ethguard.Client, []*simulatePeer) {
	t.Helper()
	if len(responses) == 0 {
		t.Fatal("simulate fleet requires at least one response")
	}
	clientConfig := config.Config{ChainID: 1}
	peers := make([]*simulatePeer, len(responses))
	for i, response := range responses {
		peer := &simulatePeer{}
		ws := i == 1 // Exercise HTTP and WSS in the same native fleet.
		peer.peer = newSourceWirePeer(t, ws, func(req sourceWireRequest) string {
			if override != nil {
				if response, handled := override(i, req); handled {
					return response
				}
			}
			switch req.Method {
			case "eth_getBlockByNumber":
				return `"result":{"number":"` + simulatePinNumber + `","hash":"` + simulatePin + `"}`
			case methods.MethodSimulateV1:
				peer.request.Store(req)
				peer.simulateRequests.Add(1)
				return `"result":` + string(response)
			default:
				return `"result":"0x1"`
			}
		})
		transport := config.TransportNativeHTTP
		if ws {
			transport = config.TransportWSS
		}
		clientConfig.Upstreams = append(clientConfig.Upstreams, config.Upstream{
			ID: fmt.Sprintf("simulate-%d", i), Endpoint: peer.peer.endpoint, Transport: transport,
		})
		peers[i] = peer
	}
	client, err := ethguard.New(clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, peers
}

func changeSimulateGasUsed(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	blocks[0]["gasUsed"] = json.RawMessage(`"0xa411"`)
	changed, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func assertSimulateCounts(t *testing.T, peers []*simulatePeer, want int32) {
	t.Helper()
	for i, peer := range peers {
		if got := peer.simulateRequests.Load(); got != want {
			t.Fatalf("peer %d eth_simulateV1 count = %d, want %d", i, got, want)
		}
	}
}

func TestSimulateV1RootViewAndRawUseNativeStrictPlan(t *testing.T) {
	assertSimulateReferenceFixture(t)
	vector := readSimulateVector(t, "ethSimulate-simple.io")
	if vector.base != "latest" || len(vector.blocks) != 1 || len(vector.blocks[0].Calls) != 2 {
		t.Fatalf("simple vector shape = base %q, blocks/calls %d/%d", vector.base, len(vector.blocks), len(vector.blocks[0].Calls))
	}
	if string(vector.blocks[0].ParentHash) != simulatePin {
		t.Fatalf("official vector parent hash = %s, want reference pin %s", vector.blocks[0].ParentHash, simulatePin)
	}
	if vector.blocks[0].Number != "0x3d" {
		t.Fatalf("official first simulated block number = %s, want 0x3d after pinned head 0x3c", vector.blocks[0].Number)
	}
	client, peers := newSimulateFleet(t, []json.RawMessage{vector.result, vector.result, vector.result})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rootBlocks, rootResult, err := client.SimulateV1(ctx, vector.request, nil)
	if err != nil || !reflect.DeepEqual(rootBlocks, vector.blocks) {
		t.Fatalf("root SimulateV1 blocks match official vector = %v, err=%v", reflect.DeepEqual(rootBlocks, vector.blocks), err)
	}
	if rootResult.Decision == nil || rootResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("root decision = %+v, want strict corroboration", rootResult.Decision)
	}
	assertSimulateCounts(t, peers, 1)

	viewBlocks, err := client.EthClient().SimulateV1(ctx, vector.request, nil)
	if err != nil || !reflect.DeepEqual(viewBlocks, vector.blocks) {
		t.Fatalf("view SimulateV1 blocks match official vector = %v, err=%v", reflect.DeepEqual(viewBlocks, vector.blocks), err)
	}
	assertSimulateCounts(t, peers, 2)

	// The generic raw Call omits the optional base argument; its plan default
	// must still use the actual anchor established for latest.
	rawResult, err := client.Call(ctx, methods.MethodSimulateV1, vector.payload)
	if err != nil {
		t.Fatalf("raw Call eth_simulateV1: %v", err)
	}
	if rawResult.Decision == nil || rawResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("raw decision = %+v, want the method's strict corroboration", rawResult.Decision)
	}
	rawBlocks, err := ethapi.NewSimulateV1Result(rawResult.Decision.Value)
	if err != nil {
		t.Fatalf("decode raw simulation result: %v", err)
	}
	if !reflect.DeepEqual(rawBlocks, vector.blocks) {
		t.Fatalf("raw result differs from official vector: got %d blocks, want %d", len(rawBlocks), len(vector.blocks))
	}
	assertSimulateCounts(t, peers, 3)

	wantPayload, err := json.Marshal(vector.request)
	if err != nil {
		t.Fatal(err)
	}
	for i, peer := range peers {
		loaded := peer.request.Load()
		if loaded == nil {
			t.Fatalf("peer %d did not receive eth_simulateV1", i)
		}
		request := loaded.(sourceWireRequest)
		if len(request.Params) != 2 || string(request.Params[0]) != string(wantPayload) {
			t.Fatalf("peer %d params = %s, want official payload and pinned base", i, request.Params)
		}
		var base string
		if err := json.Unmarshal(request.Params[1], &base); err != nil || base != simulatePin {
			t.Fatalf("peer %d simulation base = %s (%v), want actual anchor hash %s", i, request.Params[1], err, simulatePin)
		}
	}
	if peers[0].peer.httpConnections.Load() == 0 || peers[1].peer.connections.Load() == 0 {
		t.Fatalf("native transports were not both exercised: http connections=%d, wss connections=%d", peers[0].peer.httpConnections.Load(), peers[1].peer.connections.Load())
	}

	client.Close()
	if _, _, err := client.SimulateV1(ctx, vector.request, nil); !errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatalf("SimulateV1 after Close = %v, want ErrPoolClosed", err)
	}
	if _, err := client.EthClient().SimulateV1(ctx, vector.request, nil); !errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatalf("view SimulateV1 after Close = %v, want ErrPoolClosed", err)
	}
	if _, err := client.Call(ctx, methods.MethodSimulateV1, vector.payload, vector.base); !errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatalf("raw SimulateV1 after Close = %v, want ErrPoolClosed", err)
	}
	assertSimulateCounts(t, peers, 3)
}

func TestSimulateV1InnerCallFailureRemainsAResult(t *testing.T) {
	vector := readSimulateVector(t, "ethSimulate-eth-send-should-not-produce-logs-on-revert.io")
	if len(vector.blocks) != 1 || len(vector.blocks[0].Calls) != 1 {
		t.Fatalf("revert vector shape = blocks/calls %d/%d", len(vector.blocks), len(vector.blocks[0].Calls))
	}
	wantFailure := vector.blocks[0].Calls[0].ResultOfCallFailure
	if wantFailure == nil || wantFailure.Status != "0x0" || wantFailure.Error.SimulateErrorRevert == nil || wantFailure.Error.SimulateErrorRevert.Code != 3 {
		t.Fatalf("official revert vector call = %+v, want status 0x0 and EVM revert code 3", vector.blocks[0].Calls[0])
	}
	client, _ := newSimulateFleet(t, []json.RawMessage{vector.result, vector.result, vector.result})
	baseCtx, evidenceErr := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if evidenceErr != nil {
		t.Fatal(evidenceErr)
	}
	ctx, capture := ethguard.WithResult(baseCtx)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	blocks, result, err := client.SimulateV1(ctx, vector.request, nil)
	if err != nil {
		t.Fatalf("SimulateV1 returned top-level error for an inner revert: %v", err)
	}
	if !reflect.DeepEqual(blocks, vector.blocks) || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("inner revert result = blocks match:%v decision:%+v", reflect.DeepEqual(blocks, vector.blocks), result.Decision)
	}
	failure := blocks[0].Calls[0].ResultOfCallFailure
	if failure == nil || failure.Status != "0x0" || failure.Error.SimulateErrorRevert == nil || failure.Error.SimulateErrorRevert.Message != wantFailure.Error.SimulateErrorRevert.Message {
		t.Fatalf("inner revert projection = %+v, want official failure result", failure)
	}
	if strings.Contains(string(mustMarshalSimulateFailure(t, failure)), `"data"`) {
		t.Fatal("unmodeled error.data was injected into the generated failure projection")
	}
	captured, published, err := capture.Result()
	if err != nil || !published || captured.Uncertain == nil {
		t.Fatalf("raw simulation evidence published=%v err=%v uncertain-present=%v", published, err, captured.Uncertain != nil)
	}
	var simulationEvidence []ethguard.UncertainResponse
	for _, source := range captured.Uncertain.Raw {
		if source.Method == methods.MethodSimulateV1 {
			simulationEvidence = append(simulationEvidence, source)
		}
	}
	if len(simulationEvidence) != 3 {
		t.Fatalf("raw simulation evidence has %d sources, want 3", len(simulationEvidence))
	}
	for i, source := range simulationEvidence {
		if !strings.Contains(string(source.Value), `"logs":[]`) || !strings.Contains(string(source.Value), `"data":"0x08c379a000000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000019416c7761797320726576657274696e6720636f6e747261637400000000000000"`) {
			t.Fatalf("source %d raw official failure value lost inner logs or error.data: %s", i, source.Value)
		}
	}
}

func mustMarshalSimulateFailure(t *testing.T, failure *ethapi.SimulateCallFailure) []byte {
	t.Helper()
	data, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSimulateV1DisagreementDoesNotReportSuccess(t *testing.T) {
	vector := readSimulateVector(t, "ethSimulate-simple.io")
	client, _ := newSimulateFleet(t, []json.RawMessage{vector.result, changeSimulateGasUsed(t, vector.result)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blocks, result, err := client.SimulateV1(ctx, vector.request, nil)
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("SimulateV1 disagreement error = %v, want rpc.ErrConflict", err)
	}
	if blocks != nil {
		t.Fatalf("conflicting simulations returned blocks: %+v", blocks)
	}
	if result.Decision == nil || result.Decision.Status == rpc.Corroborated {
		t.Fatalf("conflicting decision = %+v, must not report corroborated success", result.Decision)
	}
}

func TestSimulateV1PendingAndForwardedModesDoNotClaimCorroboration(t *testing.T) {
	vector := readSimulateVector(t, "ethSimulate-simple.io")
	client, peers := newSimulateFleet(t, []json.RawMessage{vector.result, vector.result, vector.result})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, pendingResult, err := client.SimulateV1(ctx, vector.request, simulateReference(t, `"pending"`))
	if err != nil {
		t.Fatalf("pending SimulateV1: %v", err)
	}
	if pendingResult.Decision == nil || pendingResult.Decision.Status != rpc.Selected {
		t.Fatalf("pending decision = %+v, want node-local Selected", pendingResult.Decision)
	}
	if pendingResult.Decision.Status == rpc.Corroborated || pendingResult.Decision.BlockRef != "" {
		t.Fatalf("pending decision falsely claims a corroborated pinned reference: %+v", pendingResult.Decision)
	}
	for i, peer := range peers {
		if peer.peer.headerRequests.Load() != 0 {
			t.Fatalf("pending request established %d references at peer %d", peer.peer.headerRequests.Load(), i)
		}
		loaded := peer.request.Load()
		if loaded == nil {
			continue // Only the selected pending source is expected to be queried.
		}
		request := loaded.(sourceWireRequest)
		var base string
		if len(request.Params) != 2 || json.Unmarshal(request.Params[1], &base) != nil || base != "pending" {
			t.Fatalf("peer %d pending params = %s", i, request.Params)
		}
	}

	single, err := ethguard.DialUncorroborated(peers[0].peer.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(single.Close)
	forwarded, err := single.Call(ctx, methods.MethodSimulateV1, vector.payload, simulatePinNumber)
	if err != nil {
		t.Fatalf("single-source raw SimulateV1: %v", err)
	}
	if forwarded.Decision == nil || forwarded.Decision.Status != rpc.Forwarded || forwarded.Decision.Status == rpc.Corroborated {
		t.Fatalf("single-source decision = %+v, want Forwarded without a corroboration claim", forwarded.Decision)
	}
}

func TestSimulateV1RejectsByteSliceBlockStateCallsBeforeNetwork(t *testing.T) {
	vector := readSimulateVector(t, "ethSimulate-simple.io")
	client, peers := newSimulateFleet(t, []json.RawMessage{vector.result, vector.result, vector.result})
	before := make([]int32, len(peers))
	for i, peer := range peers {
		before[i] = peer.peer.requests.Load() // Client construction's identity probe is allowed.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Call(ctx, methods.MethodSimulateV1, map[string]any{
		"blockStateCalls": []byte{1},
	})
	if !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("byte-slice blockStateCalls error = %v, want ErrInvalidRequest", err)
	}
	for i, peer := range peers {
		if got := peer.peer.requests.Load(); got != before[i] {
			t.Fatalf("invalid payload contacted peer %d: request count %d -> %d", i, before[i], got)
		}
		if peer.peer.headerRequests.Load() != 0 || peer.simulateRequests.Load() != 0 {
			t.Fatalf("invalid payload reached business or reference route at peer %d", i)
		}
	}
}

func TestSimulateV1CancellationAfterProviderReceivesRequest(t *testing.T) {
	vector := readSimulateVector(t, "ethSimulate-simple.io")
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	client, _ := newSimulateFleetWithOverride(t, []json.RawMessage{vector.result, vector.result, vector.result},
		func(_ int, request sourceWireRequest) (string, bool) {
			if request.Method != methods.MethodSimulateV1 {
				return "", false
			}
			started <- struct{}{}
			<-release
			return `"result":` + string(vector.result), true
		})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		blocks []ethapi.SimulateBlock
		result ethguard.CallResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		blocks, result, err := client.SimulateV1(ctx, vector.request, nil)
		done <- outcome{blocks: blocks, result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("provider did not receive eth_simulateV1 before timeout")
	}
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.blocks != nil {
			t.Fatalf("canceled simulation escaped as a result: blocks=%v result=%+v err=%v", got.blocks, got.result, got.err)
		}
		if got.result.Err != got.err || got.result.Decision == nil || got.result.Decision.Status != rpc.Unknown || len(got.result.Decision.Value) != 0 { //nolint:errorlint // The result must retain the exact error returned to the caller.
			t.Fatalf("canceled simulation facts = %+v, want Unknown with no value and the same cancellation error", got.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SimulateV1 did not stop after context cancellation")
	}
	releaseOnce.Do(func() { close(release) })
}

func simulateReference(t *testing.T, raw string) *ethapi.SimulateBlockReference {
	t.Helper()
	value, err := ethapi.NewSimulateBlockReference(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return &value
}

func assertSimulateJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	decode := func(raw json.RawMessage) any {
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(got), decode(want)) {
		t.Fatalf("JSON changed: got %s, want %s", got, want)
	}
}

func TestSimulateV1OfficialSelectorsAndOpenNumbersReachWire(t *testing.T) {
	// Independent integer literals cross typed construction, preparation,
	// reference handling, transports and the prepared-request record.
	payloadJSON := json.RawMessage(`{"blockStateCalls":[{"calls":[{"vendor":{"n":9007199254740993,"deep":[18446744073709551617]}}]}],"vendorRoot":{"n":900719925474099312345}}`)
	payload, err := ethapi.NewSimulateV1Request(payloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`"0x10"`, `"earliest"`,
		`"0x0000000000000000000000000000000000000000000000000000000000000001"`,
		`"0x0000000000000000000000000000000000000000000000000000000000000000"`,
		`"0x1111111111111111111111111111111111111111111111111111111111111111"`,
		`{"blockNumber":"0x10"}`,
		`{"blockHash":"0x0000000000000000000000000000000000000000000000000000000000000001"}`,
		`{"blockHash":"0x0000000000000000000000000000000000000000000000000000000000000001","requireCanonical":false}`,
		`{"blockHash":"0x0000000000000000000000000000000000000000000000000000000000000001","requireCanonical":true}`,
	} {
		t.Run(raw, func(t *testing.T) {
			client, peers := newSimulateFleet(t, []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`[]`), json.RawMessage(`[]`)})
			base := simulateReference(t, raw)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, rootResult, err := client.SimulateV1(ctx, payload, base)
			if err != nil || rootResult.Decision == nil || rootResult.Decision.Status != rpc.Corroborated {
				t.Fatalf("root: %+v, %v", rootResult.Decision, err)
			}
			if _, err := client.EthClient().SimulateV1(ctx, payload, base); err != nil {
				t.Fatal(err)
			}
			rawResult, err := client.Call(ctx, methods.MethodSimulateV1, payloadJSON, json.RawMessage(raw))
			if err != nil || rawResult.Decision == nil || rawResult.Decision.Status != rpc.Corroborated {
				t.Fatalf("raw: %+v, %v", rawResult.Decision, err)
			}
			assertSimulateCounts(t, peers, 3)
			for _, decision := range []*rpc.Decision{rootResult.Decision, rawResult.Decision} {
				if decision.Prepared == nil {
					t.Fatal("missing prepared request")
				}
				var params []json.RawMessage
				if err := json.Unmarshal(decision.Prepared.Params, &params); err != nil {
					t.Fatal(err)
				}
				if len(params) != 2 {
					t.Fatalf("prepared params: %s", decision.Prepared.Params)
				}
				assertSimulateJSONEqual(t, params[0], payloadJSON)
				assertSimulateJSONEqual(t, params[1], json.RawMessage(raw))
			}
			for _, peer := range peers {
				if peer.peer.headerRequests.Load() != 0 {
					t.Fatal("explicit selector established a moving reference")
				}
				request := peer.request.Load().(sourceWireRequest)
				assertSimulateJSONEqual(t, request.Params[0], payloadJSON)
				assertSimulateJSONEqual(t, request.Params[1], json.RawMessage(raw))
				var prepared []json.RawMessage
				if err := json.Unmarshal(rawResult.Decision.Prepared.Params, &prepared); err != nil {
					t.Fatal(err)
				}
				for i := range prepared {
					if string(prepared[i]) != string(request.Params[i]) {
						t.Fatalf("prepared/wire differ: %s / %s", prepared[i], request.Params[i])
					}
				}
			}
		})
	}
}

func TestSimulateV1NullReferencesUseLatestDefault(t *testing.T) {
	client, peers := newSimulateFleet(t, []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`[]`), json.RawMessage(`[]`)})
	for i, base := range []any{nil, (*ethapi.SimulateBlockReference)(nil), json.RawMessage(`null`), json.RawMessage(nil)} {
		result, err := client.Call(context.Background(), methods.MethodSimulateV1, json.RawMessage(`{"blockStateCalls":[]}`), base)
		if err != nil || result.Decision == nil || result.Decision.BlockRef != simulatePin {
			t.Fatalf("null form %d: %+v %v", i, result.Decision, err)
		}
		assertSimulateCounts(t, peers, int32(i+1))
		for _, peer := range peers {
			request := peer.request.Load().(sourceWireRequest)
			if string(request.Params[1]) != `"`+simulatePin+`"` {
				t.Fatalf("null reached wire: %s", request.Params[1])
			}
		}
	}
}

func TestSimulateV1InvalidGeneratedInputsFailBeforeNetwork(t *testing.T) {
	client, peers := newSimulateFleet(t, []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`[]`), json.RawMessage(`[]`)})
	before := make([]int32, len(peers))
	for i, peer := range peers {
		before[i] = peer.peer.requests.Load()
	}
	payload := ethapi.SimulateV1Request{BlockStateCalls: []ethapi.BlockStateCall{{}}}
	badPayload := payload
	badPayload.BlockStateCalls = []ethapi.BlockStateCall{{StateOverrides: &ethapi.StateOverrides{"bad": {}}}}
	badNumber := ethapi.Quantity("latest")
	badBase := &ethapi.SimulateBlockReference{Eip1898BlockNumberObject: &ethapi.BlockNumberReference{BlockNumber: badNumber}}
	for _, invoke := range []func() error{
		func() error { _, _, err := client.SimulateV1(context.Background(), badPayload, nil); return err },
		func() error {
			_, err := client.EthClient().SimulateV1(context.Background(), badPayload, nil)
			return err
		},
		func() error {
			_, err := client.Call(context.Background(), methods.MethodSimulateV1, json.RawMessage(`{"blockStateCalls":[{"stateOverrides":{"bad":{}}}]}`))
			return err
		},
		func() error { _, _, err := client.SimulateV1(context.Background(), payload, badBase); return err },
		func() error {
			_, err := client.EthClient().SimulateV1(context.Background(), payload, badBase)
			return err
		},
		func() error {
			_, err := client.Call(context.Background(), methods.MethodSimulateV1, payload, map[string]any{"blockNumber": "latest"})
			return err
		},
	} {
		if err := invoke(); !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("invalid generated input: %v", err)
		}
	}
	for i, peer := range peers {
		if peer.peer.requests.Load() != before[i] || peer.peer.headerRequests.Load() != 0 || peer.simulateRequests.Load() != 0 {
			t.Fatalf("invalid input contacted peer %d", i)
		}
	}
}
