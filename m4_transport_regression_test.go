package ethguard_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestCreateAccessListNativeHTTPAndRawWSSTransport locks the extension's
// already-frozen pending-local wire shape.  Pending is deliberately used here
// so the test isolates HTTP/WSS request and response parity without introducing
// a new moving-head anchor contract.
func TestCreateAccessListNativeHTTPAndRawWSSTransport(t *testing.T) {
	const rawResult = `{"accessList":[],"gasUsed":"0x5208"}`
	var requests [2]atomic.Value
	answer := func(slot *atomic.Value) func(sourceWireRequest) string {
		return func(req sourceWireRequest) string {
			if req.Method == methods.MethodCreateAccessList {
				slot.Store(req)
				return `"result":` + rawResult
			}
			return `"result":"0x1"`
		}
	}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, answer(&requests[0])),
		newSourceWirePeer(t, true, answer(&requests[1])),
	}
	client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
		{ID: "http", Endpoint: peers[0].endpoint},
		{ID: "wss", Endpoint: peers[1].endpoint, Transport: config.TransportWSS},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	result, decision, err := client.CreateAccessList(ctx, map[string]any{"to": to.Hex()}, "pending")
	if err != nil || result == nil || result.GasUsed != 21000 || len(result.AccessList) != 0 {
		t.Fatalf("CreateAccessList = %+v decision=%+v err=%v", result, decision, err)
	}
	if decision.Decision == nil || decision.Decision.Status != rpc.Selected {
		t.Fatalf("decision = %+v, want selected pending observation", decision.Decision)
	}
	for i, slot := range requests {
		loaded := slot.Load()
		if loaded == nil {
			t.Fatalf("source %d did not receive eth_createAccessList", i)
		}
		req := loaded.(sourceWireRequest)
		if req.Method != methods.MethodCreateAccessList || len(req.Params) != 2 || string(req.Params[1]) != `"pending"` {
			t.Fatalf("source %d request = %+v, want call object + pending", i, req)
		}
		var call map[string]json.RawMessage
		if err := json.Unmarshal(req.Params[0], &call); err != nil {
			t.Fatalf("source %d call object: %v", i, err)
		}
		if string(call["to"]) != `"`+strings.ToLower(to.Hex())+`"` {
			t.Fatalf("source %d to = %s", i, call["to"])
		}
	}
}

func TestCreateAccessListTransportNullAndMalformedStayTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "null", body: `"result":null`},
		{name: "malformed", body: `"result":{"accessList":[{"address":"0x01"}],"gasUsed":"0x5208"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peers := []*sourceWirePeer{
				newSourceWirePeer(t, false, func(req sourceWireRequest) string {
					if req.Method == methods.MethodCreateAccessList {
						return tc.body
					}
					return `"result":"0x1"`
				}),
				newSourceWirePeer(t, true, func(req sourceWireRequest) string {
					if req.Method == methods.MethodCreateAccessList {
						return tc.body
					}
					return `"result":"0x1"`
				}),
			}
			client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
				{ID: "http", Endpoint: peers[0].endpoint},
				{ID: "wss", Endpoint: peers[1].endpoint, Transport: config.TransportWSS},
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, decision, callErr := client.CreateAccessList(ctx, map[string]any{"to": "0x0000000000000000000000000000000000000001"}, "pending")
			if result != nil || callErr == nil || decision.Decision == nil {
				t.Fatalf("%s result=%+v decision=%+v err=%v", tc.name, result, decision, callErr)
			}
		})
	}
}

// TestSendTransactionNativeHTTPAndRawWSSParity proves the typed write entry
// serializes exactly the same signed bytes as SendRawTransaction on both
// configured transports and preserves the accepted WriteDecision.
func TestSendTransactionNativeHTTPAndRawWSSParity(t *testing.T) {
	raw, hash := unifiedSignedTransaction(t)
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		t.Fatal(err)
	}
	var requests [2]atomic.Value
	answer := func(slot *atomic.Value) func(sourceWireRequest) string {
		return func(req sourceWireRequest) string {
			if req.Method == methods.MethodSendRawTransaction {
				slot.Store(req)
				return `"result":"` + hash + `"`
			}
			return `"result":"0x1"`
		}
	}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, answer(&requests[0])),
		newSourceWirePeer(t, true, answer(&requests[1])),
	}
	client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
		{ID: "http", Endpoint: peers[0].endpoint},
		{ID: "wss", Endpoint: peers[1].endpoint, Transport: config.TransportWSS},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, callErr := client.SendTransaction(ctx, tx)
	if callErr != nil || !result.IsWriteAccepted() || result.WriteDecision == nil || result.WriteDecision.Hash != strings.ToLower(hash) {
		t.Fatalf("SendTransaction result=%+v err=%v", result, callErr)
	}
	for i, slot := range requests {
		loaded := slot.Load()
		if loaded == nil {
			t.Fatalf("source %d did not receive eth_sendRawTransaction", i)
		}
		req := loaded.(sourceWireRequest)
		if len(req.Params) != 1 || string(req.Params[0]) != `"0x`+hex.EncodeToString(raw)+`"` {
			t.Fatalf("source %d params = %s", i, req.Params)
		}
	}
}
