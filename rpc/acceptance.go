package rpc

// acceptanceCalculator is the internal seam for support thresholds. It keeps
// the voting-basis arithmetic out of individual resolvers and field
// aggregation paths. The public Corroboration snapshot remains the migration
// input for now; T07 will move its default construction behind this seam.
type acceptanceCalculator struct {
	floor int
}

func newAcceptanceCalculator(c Corroboration) acceptanceCalculator {
	return acceptanceCalculator{floor: c.Floor}
}

// required returns the support needed for a fixed basis. Every member of the
// basis counts, including failures and members that were not contacted. A
// configured floor is an integrity lower bound; strict majority remains the
// dynamic bound as the basis changes.
func (a acceptanceCalculator) required(basis int) int {
	if basis <= 0 {
		// Keep the historical positive quorum contract for an empty
		// observation set; callers still receive no fields, while the
		// generic aggregator does not turn an empty set into an invalid
		// quorum error.
		return 1
	}
	required := basis/2 + 1
	if a.floor > required {
		required = a.floor
	}
	return required
}

func (a acceptanceCalculator) accepts(basis, support int) bool {
	return basis > 0 && support >= a.required(basis)
}
