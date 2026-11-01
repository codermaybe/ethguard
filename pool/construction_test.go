package pool

// join is a test-only construction helper. Production admits fixed Members
// with their selected connection factories through NewWithMembers.
func (p *Pool) join(name, endpoint string) (*Source, error) {
	return p.admit(Member{Name: name, URL: endpoint})
}
