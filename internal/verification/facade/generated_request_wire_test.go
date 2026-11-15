package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

const generatedCallPolicyRequest = `{"to":null,"gas":"0x5208","input":"0x0001","accessList":[],"commitments":[],"proofs":[],"authorizationList":[{"address":"0x0000000000000000000000000000000000000001","chainId":"0x1","nonce":"0x0","r":"0x1","s":"0x2","yParity":"0x0","vendor":{"integer":900719925474099312345678901234567890,"nullable":null},"optional":null}]}`

func TestGeneratedRequestsPreserveAuthorizationOnHTTPAndWSSAtHash(t *testing.T) {
	var records [2][2]atomic.Value // source, then call/estimate
	peers := make([]*sourceWirePeer, 2)
	for i := range peers {
		peers[i] = newSourceWirePeer(t, i == 1, func(req sourceWireRequest) string {
			switch req.Method {
			case methods.MethodCall:
				records[i][0].Store(req)
				return `"result":"0x6000"`
			case methods.MethodEstimateGas:
				records[i][1].Store(req)
				return `"result":"0x5208"`
			default:
				t.Errorf("explicit hash call unexpectedly requested %s", req.Method)
				return `"result":"0x1"`
			}
		})
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
	request, err := ethapi.NewCallMsg(json.RawMessage(generatedCallPolicyRequest))
	if err != nil {
		t.Fatal(err)
	}
	hash := ethapi.Hash("0x" + strings.Repeat("11", 32))
	data, result, err := client.CallContractAtHash(ctx, request, hash)
	if err != nil || string(data) != "\x60\x00" || !result.IsCorroborated() {
		t.Fatalf("CallContractAtHash = %x, %+v, %v", data, result, err)
	}
	gas, err := client.EthClient().EstimateGasAtBlockHash(ctx, request, hash)
	if err != nil || gas != 21000 {
		t.Fatalf("view EstimateGasAtBlockHash = %d, %v", gas, err)
	}
	for i, peer := range peers {
		if peer.headerRequests.Load() != 0 {
			t.Fatalf("source %d established an anchor for an explicit hash", i)
		}
		for method := range records[i] {
			loaded := records[i][method].Load()
			if loaded == nil {
				t.Fatalf("source %d did not receive method index %d", i, method)
			}
			req := loaded.(sourceWireRequest)
			if len(req.Params) != 2 || string(req.Params[1]) != `{"blockHash":"`+string(hash)+`","requireCanonical":false}` {
				t.Fatalf("source %d method %s reference = %s", i, req.Method, req.Params)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(req.Params[0], &object); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"to": "null", "gas": `"0x5208"`, "input": `"0x0001"`, "accessList": "[]", "commitments": "[]", "proofs": "[]"} {
				if string(object[key]) != want {
					t.Errorf("source %d %s.%s = %s, want %s", i, req.Method, key, object[key], want)
				}
			}
			var authorizations []map[string]json.RawMessage
			if err := json.Unmarshal(object["authorizationList"], &authorizations); err != nil || len(authorizations) != 1 {
				t.Fatalf("source %d authorizationList = %s, %v", i, object["authorizationList"], err)
			}
			for key, want := range map[string]string{
				"address": `"0x0000000000000000000000000000000000000001"`, "chainId": `"0x1"`, "nonce": `"0x0"`, "r": `"0x1"`, "s": `"0x2"`, "yParity": `"0x0"`,
				"vendor": `{"integer":900719925474099312345678901234567890,"nullable":null}`, "optional": "null",
			} {
				if string(authorizations[0][key]) != want {
					t.Errorf("source %d %s authorization.%s = %s, want %s", i, req.Method, key, authorizations[0][key], want)
				}
			}
		}
	}
}

func TestGeneratedInvalidTypedRequestsFailBeforeFanout(t *testing.T) {
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":"0x1"` }),
		newSourceWirePeer(t, true, func(sourceWireRequest) string { return `"result":"0x1"` }),
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
	for _, mutation := range []string{"invalid-address", "extra-collision", "extra-invalid-json", "extra-duplicate-json"} {
		t.Run(mutation, func(t *testing.T) {
			request, err := ethapi.NewCallMsg(json.RawMessage(generatedCallPolicyRequest))
			if err != nil {
				t.Fatal(err)
			}
			authorization := &(*request.AuthorizationList)[0]
			switch mutation {
			case "invalid-address":
				authorization.Address = ethapi.Address("0x1")
			case "extra-collision":
				authorization.AdditionalProperties = map[string]json.RawMessage{"nonce": json.RawMessage(`"0x0"`)}
			case "extra-invalid-json":
				authorization.AdditionalProperties = map[string]json.RawMessage{"extra": json.RawMessage(`invalid`)}
			case "extra-duplicate-json":
				authorization.AdditionalProperties = map[string]json.RawMessage{"extra": json.RawMessage(`{"x":1,"x":2}`)}
			}
			before := []int32{peers[0].requests.Load(), peers[1].requests.Load()}
			zeroHash := ethapi.Hash("0x" + strings.Repeat("0", 64))
			data, _, callErr := client.CallContractAtHash(ctx, request, zeroHash)
			gas, _, estimateErr := client.EstimateGasAtBlockHash(ctx, request, zeroHash)
			if data != nil || gas != 0 || !errors.Is(callErr, rpc.ErrInvalidRequest) || !errors.Is(estimateErr, rpc.ErrInvalidRequest) {
				t.Fatalf("invalid typed request = data:%x gas:%d call:%v estimate:%v", data, gas, callErr, estimateErr)
			}
			for i, peer := range peers {
				if got := peer.requests.Load(); got != before[i] {
					t.Fatalf("source %d contacted before local rejection: %d -> %d", i, before[i], got)
				}
			}
		})
	}
}
