package rpc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The admission threshold table pins Q = max(H, floor(n/2)+1) for every basis
// size the client can present, including the degenerate empty set. See
// docs/design/acceptance-thresholds.md section 1.1.
func TestAdmissionRequiredSupportTable(t *testing.T) {
	cases := []struct {
		name  string
		floor int
		n     int
		want  int
	}{
		{"empty basis keeps the positive-quorum contract", 2, 0, 1},
		{"one member with the default floor", 2, 1, 2},
		{"two members majority of two", 2, 2, 2},
		{"three members majority of three", 2, 3, 2},
		{"four members majority of four", 2, 4, 3},
		{"five members majority of five", 2, 5, 3},
		{"six members majority of six", 2, 6, 4},
		{"floor above majority binds", 4, 5, 4},
		{"floor below majority never lowers it", 1, 5, 3},
		{"zero floor degrades to pure majority", 0, 5, 3},
		{"zero floor with two members", 0, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			corroboration := Corroboration{Mode: CorroborationMajority, Floor: tc.floor}
			if got := corroboration.Required(tc.n); got != tc.want {
				t.Fatalf("Required(%d) with floor %d = %d, want %d", tc.n, tc.floor, got, tc.want)
			}
		})
	}
}

// A single supporter is Selected even where the floor would let a
// corroboration claim through: corroboration needs two independent sources.
func TestAdmissionSingleSupporterStaysSelected(t *testing.T) {
	corroboration := Corroboration{Mode: CorroborationMajority, Floor: 1}
	single := []cluster{{key: "k", sources: []SourceRef{{ID: "a"}}}}
	decision, err := corroboration.adjudicate(1, single, nil)
	if err != nil {
		t.Fatalf("adjudicate: %v", err)
	}
	if decision.Status != Selected {
		t.Fatalf("single supporter status = %s, want selected", decision.Status)
	}
}

// Unanimity adds agreement over the same bound; a split fleet fails even when
// the winning cluster holds the majority.
func TestAdmissionUnanimityRejectsSplitFleet(t *testing.T) {
	corroboration := Corroboration{Mode: CorroborationUnanimity, Floor: 2}
	ranked := []cluster{
		{key: "a", sources: []SourceRef{{ID: "a"}, {ID: "b"}}},
		{key: "b", sources: []SourceRef{{ID: "c"}}},
	}
	_, err := corroboration.adjudicate(3, ranked, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("adjudicate error = %v, want ErrConflict", err)
	}
}

// A minority dissent does not fail the majority mode; the losing cluster is
// recorded as rejection evidence.
func TestAdmissionMajorityToleratesMinorityDissent(t *testing.T) {
	corroboration := Corroboration{Mode: CorroborationMajority, Floor: 2}
	ranked := []cluster{
		{key: "a", sources: []SourceRef{{ID: "a"}, {ID: "b"}, {ID: "c"}}},
		{key: "b", sources: []SourceRef{{ID: "d"}}},
	}
	decision, err := corroboration.adjudicate(4, ranked, nil)
	if err != nil {
		t.Fatalf("adjudicate: %v", err)
	}
	if decision.Status != Corroborated {
		t.Fatalf("status = %s, want corroborated", decision.Status)
	}
	if len(decision.Reject) != 1 || decision.Reject[0].ID != "d" {
		t.Fatalf("reject evidence = %v, want source d", decision.Reject)
	}
}

// The floor cannot be satisfied by a shrinking denominator: failures and
// skipped members stay in the basis, so a partially delivered fan-out fails
// instead of corroborating over survivors.
func TestAdmissionDenominatorNeverShrinks(t *testing.T) {
	corroboration := Corroboration{Mode: CorroborationMajority, Floor: 2}
	// Four voting members, only two delivered and agreed: the denominator is
	// still four, and the required support is three.
	ranked := []cluster{{key: "a", sources: []SourceRef{{ID: "a"}, {ID: "b"}}}}
	_, err := corroboration.adjudicate(4, ranked, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("adjudicate error = %v, want ErrConflict", err)
	}
	required := corroboration.Required(4)
	if required != 3 {
		t.Fatalf("required over four-member basis = %d, want 3", required)
	}
}

// Forwarded mode has no corroboration claim: Required is still defined for
// diagnostics, but the coordinator never adjudicates a forwarded read.
func TestAdmissionForwardedModeHasNoFloor(t *testing.T) {
	corroboration := Corroboration{Mode: CorroborationForwarded, Floor: 0}
	if got := corroboration.Required(3); got != 2 {
		t.Fatalf("forwarded Required(3) = %d, want pure majority 2", got)
	}
}

// Error text names the required support and the whole basis so operators see
// why a fan-out failed without recomputing the formula.
func TestAdmissionFailureNamesRequiredAndBasis(t *testing.T) {
	corroboration := Corroboration{Mode: CorroborationMajority, Floor: 2}
	ranked := []cluster{{key: "a", sources: []SourceRef{{ID: "a"}}}}
	_, err := corroboration.adjudicate(5, ranked, nil)
	if err == nil {
		t.Fatal("one supporter over a five-member basis must fail")
	}
	want := fmt.Sprintf("required support %d, winning cluster %d of %d voting members", 3, 1, 5)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not name required support and basis (%q)", err, want)
	}
}
