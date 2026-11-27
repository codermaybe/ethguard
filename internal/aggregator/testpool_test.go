package aggregator

import (
	"testing"

	"github.com/codermaybe/ethguard/internal/pool"
)

// fixedPool builds a pool with construction-time membership. Tests that only
// need a settled member set use this instead of mutating a live pool.
func fixedPool(tb testing.TB, members ...pool.Member) *pool.Pool {
	tb.Helper()
	return fixedPoolWith(tb, 0, nil, members...)
}

func fixedPoolWith(tb testing.TB, chainID uint64, opts []pool.PoolOption, members ...pool.Member) *pool.Pool {
	tb.Helper()
	p, err := pool.NewWithMembers(chainID, members, opts...)
	if err != nil {
		tb.Fatalf("NewWithMembers: %v", err)
	}
	tb.Cleanup(p.Close)
	return p
}
