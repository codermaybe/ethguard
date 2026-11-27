package aggregator

import (
	"testing"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// TestPreparedBusinessRoundTipScope proves the business collection's scope is
// derived from the prepared request, not the original tag: a rewritten pin is
// exempt, while a request that still carries latest keeps the latest gate.
// Safe/finalized are exempt because latest block-number samples do not
// describe their freshness. The scope is computed once from the plan path's
// prepared request — the only execution path the registry drives — and
// freezes into the invocation.
func TestPreparedBusinessRoundTipScope(t *testing.T) {
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		prepared rpc.Request
		want     pool.TipFreshnessScope
	}{
		{"pinned hash", rpc.Request{Method: probeMethod, Params: []any{"0x1", "0xabc"}}, pool.TipFreshnessExempt},
		{"pinned height", rpc.Request{Method: probeMethod, Params: []any{"0x1", "0x10"}}, pool.TipFreshnessExempt},
		{"pending", rpc.Request{Method: probeMethod, Params: []any{"0x1", "pending"}}, pool.TipFreshnessExempt},
		{"latest remains", rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}}, pool.TipFreshnessLatest},
		{"safe remains", rpc.Request{Method: probeMethod, Params: []any{"0x1", "safe"}}, pool.TipFreshnessExempt},
		{"finalized remains", rpc.Request{Method: probeMethod, Params: []any{"0x1", "finalized"}}, pool.TipFreshnessExempt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := preparedTipFreshness(&plan, tc.prepared); got != tc.want {
				t.Fatalf("prepared scope = %v, want %v", got, tc.want)
			}
		})
	}
}
