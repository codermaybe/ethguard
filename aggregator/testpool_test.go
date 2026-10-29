package aggregator

import (
	"testing"

	"github.com/codermaybe/ethguard/pool"
)

// fixedPool builds a pool with construction-time membership. Tests that only
// need a settled member set use this instead of mutating a live pool.
func fixedPool(t *testing.T, members ...pool.Member) *pool.Pool {
	t.Helper()
	return fixedPoolWith(t, 0, nil, members...)
}

func fixedPoolWith(t *testing.T, chainID uint64, opts []pool.PoolOption, members ...pool.Member) *pool.Pool {
	t.Helper()
	p, err := pool.NewWithMembers(chainID, members, opts...)
	if err != nil {
		t.Fatalf("NewWithMembers: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}
