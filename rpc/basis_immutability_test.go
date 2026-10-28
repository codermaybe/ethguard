package rpc

import (
	"reflect"
	"testing"
)

func TestFreezeBasisCopiesMembers(t *testing.T) {
	members := []SourceRef{{Name: "a", ID: "a"}, {Name: "b", ID: "b"}}
	basis := FreezeBasis(members, Corroboration{Mode: CorroborationMajority, Floor: 2})
	members[0].Name = "mutated"
	if basis.Members[0].Name != "a" {
		t.Fatalf("frozen basis shares the caller's member slice: %v", basis.Members)
	}
	basis.Members[1].Name = "mutated"
	if members[1].Name != "b" {
		t.Fatalf("mutating a frozen basis leaked into the caller's slice: %v", members)
	}
}

func TestBasisSizeCountsEveryMember(t *testing.T) {
	basis := FreezeBasis([]SourceRef{{ID: "a"}, {ID: "b"}, {ID: "c"}}, Corroboration{Mode: CorroborationMajority, Floor: 2})
	if basis.Size() != 3 {
		t.Fatalf("basis size = %d, want 3", basis.Size())
	}
	// Majority over a three-member basis: two supporters.
	if got, want := basis.Required(), 2; got != want {
		t.Fatalf("basis required = %d, want %d", got, want)
	}
	// The floor is an integrity bound: a five-member basis with H=4 needs four.
	basis = FreezeBasis([]SourceRef{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}, Corroboration{Mode: CorroborationMajority, Floor: 4})
	if got, want := basis.Required(), 4; got != want {
		t.Fatalf("floored basis required = %d, want %d", got, want)
	}
}

func TestBasisSortedMembersOrderByID(t *testing.T) {
	basis := FreezeBasis([]SourceRef{
		{Name: "charlie", ID: "c"},
		{Name: "alpha", ID: "a"},
		{Name: "bravo", ID: "b"},
	}, Corroboration{Mode: CorroborationMajority, Floor: 2})
	got := basis.SortedMembers()
	want := []SourceRef{{Name: "alpha", ID: "a"}, {Name: "bravo", ID: "b"}, {Name: "charlie", ID: "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted members = %v, want %v", got, want)
	}
	// Sorting is a diagnostic view; the captured order is unchanged.
	if basis.Members[0].ID != "c" {
		t.Fatalf("SortedMembers reordered the captured basis: %v", basis.Members)
	}
}

func TestBasisCloneDoesNotShareMembers(t *testing.T) {
	basis := FreezeBasis([]SourceRef{{ID: "a"}, {ID: "b"}}, Corroboration{})
	clone := basis.Clone()
	clone.Members[0].ID = "mutated"
	if basis.Members[0].ID != "a" {
		t.Fatalf("basis clone shares its member slice: %v", basis.Members)
	}
}
