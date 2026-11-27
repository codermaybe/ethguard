package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpctest"
)

func subscriptionRejectionCall(t *testing.T, client *ethguard.Client, method string, view, invalidPolicy bool) error {
	t.Helper()
	ctx := context.Background()
	p := logsFacadePolicy()
	if invalidPolicy {
		p.MemoryBytes = 0
		p.Head.MemoryBytes = 0
	}
	query, err := ethapi.NewFilterQuery(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	switch method {
	case "heads":
		var s *ethguard.HeadSubscription
		if view {
			s, err = client.EthClient().SubscribeNewHead(ctx, p.Head)
		} else {
			s, err = client.SubscribeNewHead(ctx, p.Head)
		}
		if s != nil {
			_ = s.Close()
			t.Error("local rejection created Head handle")
		}
	case "logs":
		var s *ethguard.LogsSubscription
		if view {
			s, err = client.EthClient().SubscribeFilterLogs(ctx, query, p)
		} else {
			s, err = client.SubscribeFilterLogs(ctx, query, p)
		}
		if s != nil {
			_ = s.Close()
			t.Error("local rejection created Logs handle")
		}
	case "receipts":
		rp := receiptsFacadePolicy()
		if invalidPolicy {
			rp.MemoryBytes = 0
		}
		var s *ethguard.ReceiptsSubscription
		if view {
			s, err = client.EthClient().SubscribeTransactionReceipts(ctx, nil, rp)
		} else {
			s, err = client.SubscribeTransactionReceipts(ctx, nil, rp)
		}
		if s != nil {
			_ = s.Close()
			t.Error("local rejection created Receipts handle")
		}
	default:
		t.Fatal("unknown method")
	}
	return err
}

func TestClosedClientRejectsAllSubscriptionEntrypointsBeforeNetwork(t *testing.T) {
	for _, view := range []bool{false, true} {
		for _, method := range []string{"heads", "logs", "receipts"} {
			t.Run(fmt.Sprintf("view=%t/%s", view, method), func(t *testing.T) {
				a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
				b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
				client := subscriptionClient(t, a, b)
				client.Close()
				beforeA, beforeB := len(a.requestedMethods()), len(b.requestedMethods())
				if err := subscriptionRejectionCall(t, client, method, view, false); !errors.Is(err, ethguard.ErrPoolClosed) {
					t.Fatalf("closed subscription: %v, want ErrPoolClosed", err)
				}
				if len(a.requestedMethods()) != beforeA || len(b.requestedMethods()) != beforeB {
					t.Fatal("closed client made network request")
				}
			})
		}
	}
}

func TestSubscriptionCapabilityAndPolicyRejectionsAreLocal(t *testing.T) {
	for _, shape := range []string{"http-only", "one-wss-strict", "invalid-policy"} {
		for _, view := range []bool{false, true} {
			for _, method := range []string{"heads", "logs", "receipts"} {
				t.Run(fmt.Sprintf("%s/view=%t/%s", shape, view, method), func(t *testing.T) {
					http := rpctest.NewNode(1)
					t.Cleanup(http.Close)
					ws := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
					cfg := config.Config{ChainID: 1, Upstreams: []config.Upstream{{ID: "http", Endpoint: http.URL(), Transport: config.TransportNativeHTTP}}}
					if shape != "http-only" {
						cfg.Upstreams = append(cfg.Upstreams, config.Upstream{ID: "ws", Endpoint: ws.endpoint, Transport: config.TransportWSS})
					}
					var second *subscriptionPeer
					if shape == "invalid-policy" {
						second = newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
						cfg.Upstreams = append(cfg.Upstreams, config.Upstream{ID: "ws2", Endpoint: second.endpoint, Transport: config.TransportWSS})
					}
					client, err := ethguard.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					httpBefore, wsBefore := len(http.Calls()), len(ws.requestedMethods())
					err = subscriptionRejectionCall(t, client, method, view, shape == "invalid-policy")
					if !errors.Is(err, ethguard.ErrInvalidRequest) {
						t.Fatalf("local rejection: %v", err)
					}
					client.Close()
					if len(http.Calls()) != httpBefore || len(ws.requestedMethods()) != wsBefore {
						t.Fatal("local rejection contacted provider")
					}
					if second != nil && len(second.requestedMethods()) != 0 {
						t.Fatal("invalid policy contacted second provider")
					}
				})
			}
		}
	}
}
