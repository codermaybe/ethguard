package ethguard

import (
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
)

// The composition root owns the corroboration default: a multi-upstream
// client runs majority with the support floor of two (decision 0029), a
// single-upstream client forwards with no floor (decision 0030). The
// derivation lives in internal/runtime's composition; these facade tests pin
// it end to end, so a silent change to either default fails here first. The
// decision record still names a config-level default symbol that no longer
// exists; see docs/design/decision-implementation-index.md (0029 pending
// review).
func TestDefaultCorroborationFloorIsTwo(t *testing.T) {
	client, err := New(config.Config{Upstreams: []config.Upstream{
		{ID: "a", Endpoint: "http://a.example"},
		{ID: "b", Endpoint: "http://b.example"},
	}})
	if err != nil {
		t.Fatalf("multi-upstream client construction failed: %v", err)
	}
	t.Cleanup(client.Close)
	snap := client.corrob.Snapshot()
	if snap.Mode != rpc.CorroborationMajority {
		t.Fatalf("mode = %v, want majority", snap.Mode)
	}
	if snap.Floor != 2 {
		t.Fatalf("support floor = %d, want the frozen default 2", snap.Floor)
	}
	if client.Mode() != rpc.CorroborationMajority {
		t.Fatalf("public mode = %v, want majority", client.Mode())
	}
}

func TestSingleUpstreamCorroborationForwardsWithoutFloor(t *testing.T) {
	client, err := New(config.Config{Upstreams: []config.Upstream{
		{ID: "single", Endpoint: "http://a.example"},
	}})
	if err != nil {
		t.Fatalf("single-upstream client construction failed: %v", err)
	}
	t.Cleanup(client.Close)
	snap := client.corrob.Snapshot()
	if snap.Mode != rpc.CorroborationForwarded {
		t.Fatalf("mode = %v, want forwarded", snap.Mode)
	}
	if snap.Floor != 0 {
		t.Fatalf("support floor = %d, want 0 in forwarded mode", snap.Floor)
	}
}
