package ethguard_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The delivery-configuration baseline for one corroborated balance read: the
// same anchored read runs through the root facade and the ethclient-shaped
// view under the delivery configurations a caller can choose — no capture or
// evidence, a bound ResultCapture, configured source evidence without a
// capture, and evidence delivered through a capture. The evidence-without-
// capture view row is the diagnostic the delivery contract cares about: the
// view omits collection for having no consumer, and the row quantifies what
// the omission actually saves against the native row that delivers on its
// returned result.
//
// The sources are scripted same-process nodes, so absolute times carry no
// provider-network meaning; the value is the same-harness cross-configuration
// comparison and the allocation profile of the delivery layer. Each
// configuration derives its per-call context inside the measured loop —
// WithResult is invocation-scoped, so per-call derivation is the real usage,
// not overhead padding.
//
// The upstream request count per operation is reported alongside: every row
// rides the same anchored shape (one establishment round plus one business
// round per member), so a request-count drift between rows would mean the
// rows stopped measuring the same read.

// balanceDeliveryFleet builds a three-member fleet whose anchor
// establishment and balance read answer deterministically.
func balanceDeliveryFleet(b *testing.B) (*ethguard.Client, []*rpctest.Node) {
	b.Helper()
	client, nodes := benchFleet(b, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			var ref string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &ref)
			}
			var full bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &full)
			}
			if full || ref != "latest" {
				return nil, rpctest.RPCError(-32602, "unexpected anchor probe "+ref)
			}
			return map[string]any{"number": "0x10", "hash": "0x1111111111111111111111111111111111111111111111111111111111111111"}, nil
		})
		node.Return(methods.MethodGetBalance, "0x64")
	}
	return client, nodes
}

// balanceDeliveryConfig is one caller-chosen delivery configuration.
type balanceDeliveryConfig struct {
	name     string
	evidence bool
	capture  bool
}

func benchmarkBalanceAtDelivery(b *testing.B, view bool, cfg balanceDeliveryConfig) {
	client, nodes := balanceDeliveryFleet(b)
	account := ethapi.Address("0x0000000000000000000000000000000000000dad")
	run := func(ctx context.Context) error {
		if view {
			_, err := client.EthClient().BalanceAt(ctx, account, nil)
			return err
		}
		_, _, err := client.BalanceAt(ctx, account, nil)
		return err
	}

	requestsBefore := 0
	for _, node := range nodes {
		requestsBefore += len(node.Calls())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		if cfg.evidence {
			derived, err := ethguard.WithSourceEvidence(ctx, 1<<20)
			if err != nil {
				b.Fatal(err)
			}
			ctx = derived
		}
		if cfg.capture {
			ctx, _ = ethguard.WithResult(ctx)
		}
		if err := run(ctx); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	requestsAfter := 0
	for _, node := range nodes {
		requestsAfter += len(node.Calls())
	}
	b.ReportMetric(float64(requestsAfter-requestsBefore)/float64(b.N), "upstream-requests/op")
}

// BenchmarkBalanceAtDeliveryConfigurations runs the anchored balance read
// through both facades and every caller-chosen delivery configuration.
func BenchmarkBalanceAtDeliveryConfigurations(b *testing.B) {
	for _, surface := range []struct {
		name string
		view bool
	}{
		{"native", false},
		{"view", true},
	} {
		for _, cfg := range []balanceDeliveryConfig{
			{name: "no-capture-no-evidence"},
			{name: "result-capture", capture: true},
			{name: "source-evidence-no-capture", evidence: true},
			{name: "source-evidence-with-capture", evidence: true, capture: true},
		} {
			b.Run(surface.name+"/"+cfg.name, func(b *testing.B) {
				benchmarkBalanceAtDelivery(b, surface.view, cfg)
			})
		}
	}
}
