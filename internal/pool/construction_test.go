package pool

import "testing"

// join is a test-only construction helper. Production admits fixed Members
// with their selected connection factories through NewWithMembers.
func (p *Pool) join(name, endpoint string) (*Source, error) {
	return p.admit(Member{Name: name, URL: endpoint})
}

// testPool constructs an initially empty white-box fixture. All production
// initialization still runs through the fixed-membership constructor.
func testPool(tb testing.TB, chainID uint64, options ...PoolOption) *Pool {
	tb.Helper()
	p, err := NewWithMembers(chainID, nil, options...)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(p.Close)
	return p
}
