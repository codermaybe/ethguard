package reference

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// finalizedThresholdCase describes one first-round emission set for the
// establishment-threshold table: every case pins how failures, lagging views,
// and the configured quorum shape the required support and the candidate
// height. See docs/design/acceptance-thresholds.md section 1.2.
type finalizedThresholdCase struct {
	name string
	// rounds are (height, hash) pairs; a failing member is described by
	// failAt (source index) instead.
	heights []string
	hashes  []string
	failAt  []int
	maxLag  uint64
	quorum  int
	// wantRequired is the frozen establishment requirement, asserted through
	// the failure message on shortfall or through the confirmation behavior.
	wantRequired    int
	wantCandidate   string
	wantErr         error
	wantErrContains string
}

func runFinalizedThresholdCase(t *testing.T, tc finalizedThresholdCase) {
	t.Helper()
	emissions := make([]HeadObservation, 0, len(tc.heights))
	for i, height := range tc.heights {
		source := rpc.SourceRef{Name: fmt.Sprintf("s%d", i), ID: fmt.Sprintf("s%d", i)}
		raw := headJSON(height, tc.hashes[i])
		if containsInt(tc.failAt, i) {
			emissions = append(emissions, HeadObservation{Source: source, Err: errFailed, Attempted: true})
			continue
		}
		emissions = append(emissions, HeadObservation{Source: source, Value: raw, Attempted: true})
	}
	var views []HeadView
	for _, emission := range emissions {
		if emission.Err != nil {
			continue
		}
		v, err := fakeHeadCodec{}.Decode(emission.Value)
		if err != nil {
			t.Fatalf("decode %s: %v", emission.Value, err)
		}
		v.Source = emission.Source
		views = append(views, v)
	}
	var evidence rpc.ReferenceEvidence
	sel, err := selectCandidate(emissions, views, tc.maxLag, tc.quorum, &evidence)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("selectCandidate error = %v, want %v", err, tc.wantErr)
		}
		if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
			t.Fatalf("error %q does not mention %q", err, tc.wantErrContains)
		}
		return
	}
	if err != nil {
		t.Fatalf("selectCandidate: %v", err)
	}
	if sel.required != tc.wantRequired {
		t.Fatalf("frozen required = %d, want %d", sel.required, tc.wantRequired)
	}
	if fmt.Sprintf("%d", sel.candidate) != tc.wantCandidate {
		t.Fatalf("candidate height = %d, want %s", sel.candidate, tc.wantCandidate)
	}
}

func containsInt(list []int, want int) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

var errFailed = errors.New("upstream failed")

// The establishment-threshold table. Failures count into the majority bound
// over first-round emissions and shrink the fresh set; the hard floor is two;
// quorum=0 keeps the pre-quorum legacy behavior.
func TestFinalizedEstablishmentThresholdTable(t *testing.T) {
	cases := []finalizedThresholdCase{
		{
			// Five members, one failed: the majority bound counts the
			// failure (5/2+1 = 3) and stays at three even though only four
			// fresh views survive.
			name:          "failures count into the majority bound",
			heights:       []string{"0x10", "0x10", "0x10", "0x10", "0x10"},
			hashes:        []string{"0xaaa", "0xaaa", "0xaaa", "0xaaa", "0xaaa"},
			failAt:        []int{4},
			maxLag:        32,
			quorum:        2,
			wantRequired:  3,
			wantCandidate: "16",
		},
		{
			// Two failures over five members: majority bound three, fresh
			// three — the establishment barely holds and the requirement
			// stays three.
			name:          "fresh shrinkage does not lower the requirement",
			heights:       []string{"0x10", "0x10", "0x10", "0x10", "0x10"},
			hashes:        []string{"0xaaa", "0xaaa", "0xaaa", "0xaaa", "0xaaa"},
			failAt:        []int{3, 4},
			maxLag:        32,
			quorum:        2,
			wantRequired:  3,
			wantCandidate: "16",
		},
		{
			// Three failures over five members: fresh two, requirement three
			// — the establishment fails instead of corroborating over
			// survivors.
			name:            "establishment fails when fresh falls below the majority bound",
			heights:         []string{"0x10", "0x10", "0x10", "0x10", "0x10"},
			hashes:          []string{"0xaaa", "0xaaa", "0xaaa", "0xaaa", "0xaaa"},
			failAt:          []int{2, 3, 4},
			maxLag:          32,
			quorum:          2,
			wantErr:         rpc.ErrConflict,
			wantErrContains: "insufficient finalized support: need 3, have 2",
		},
		{
			// Two members: the hard floor of two is the binding constraint.
			name:          "hard floor of two members",
			heights:       []string{"0x10", "0x10"},
			hashes:        []string{"0xaaa", "0xaaa"},
			maxLag:        32,
			quorum:        0,
			wantRequired:  2,
			wantCandidate: "16",
		},
		{
			// One member can never establish a common reference, whatever
			// the configuration says.
			name:            "single member fails the hard floor",
			heights:         []string{"0x10"},
			hashes:          []string{"0xaaa"},
			maxLag:          32,
			quorum:          1,
			wantErr:         rpc.ErrConflict,
			wantErrContains: "insufficient finalized support: need 2, have 1",
		},
		{
			// A member trailing the lower median beyond maxLag leaves the
			// fresh set and is recorded as a fault.
			name:          "lagging member leaves the fresh set",
			heights:       []string{"0x10", "0x10", "0x10", "0x8"},
			hashes:        []string{"0xaaa", "0xaaa", "0xaaa", "0xbbb"},
			maxLag:        4,
			quorum:        2,
			wantRequired:  3,
			wantCandidate: "16",
		},
		{
			// A higher member above the candidate height becomes a
			// confirmation target, not a rejection.
			name:          "higher member becomes a confirmation target",
			heights:       []string{"0x10", "0x10", "0x11"},
			hashes:        []string{"0xaaa", "0xaaa", "0xccc"},
			maxLag:        32,
			quorum:        2,
			wantRequired:  2,
			wantCandidate: "16",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFinalizedThresholdCase(t, tc)
		})
	}
}

// Two distinct hashes at the candidate height are a fleet dissent over what
// "finalized" means; the table pins that this fails closed with the conflict
// sentinel before any confirmation round runs.
func TestFinalizedEstablishmentCandidateHashUniqueness(t *testing.T) {
	views := []HeadView{
		{Source: rpc.SourceRef{ID: "a"}, Height: 16, Hash: "0xaaa"},
		{Source: rpc.SourceRef{ID: "b"}, Height: 16, Hash: "0xbbb"},
		{Source: rpc.SourceRef{ID: "c"}, Height: 16, Hash: "0xaaa"},
	}
	var evidence rpc.ReferenceEvidence
	if _, err := selectCandidate(nil, views, 32, 2, &evidence); !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// The post-resolve check asserts the same frozen requirement selectCandidate
// computed; this pins that the establishment requirement survives the resolve
// unchanged (the manual check is the same value's assertion, not a second
// formula).
func TestFinalizedEstablishmentRequirementSurvivesResolve(t *testing.T) {
	// With quorum zero the requirement is the fresh count; with quorum two
	// over three first-round emissions it is max(2, 2, 2) = 2.
	heights := []string{"0x10", "0x10", "0x11"}
	hashes := []string{"0xaaa", "0xaaa", "0xccc"}
	emissions := make([]HeadObservation, 0, len(heights))
	var views []HeadView
	for i := range heights {
		source := rpc.SourceRef{Name: fmt.Sprintf("s%d", i), ID: fmt.Sprintf("s%d", i)}
		raw := headJSON(heights[i], hashes[i])
		emissions = append(emissions, HeadObservation{Source: source, Value: raw, Attempted: true})
		v, err := fakeHeadCodec{}.Decode(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		v.Source = source
		views = append(views, v)
	}
	var evidence rpc.ReferenceEvidence
	sel, err := selectCandidate(emissions, views, 32, 2, &evidence)
	if err != nil {
		t.Fatalf("selectCandidate: %v", err)
	}
	if sel.required != 2 {
		t.Fatalf("frozen required = %d, want 2", sel.required)
	}
	// The frozen basis keeps every first-round participant in the denominator.
	if len(sel.observations) != len(emissions) {
		t.Fatalf("frozen basis size = %d, want %d", len(sel.observations), len(emissions))
	}
	// The higher member was named for confirmation and carries no value yet.
	if len(sel.missing) != 1 || sel.missing[0] != "s2" {
		t.Fatalf("confirmation targets = %v, want [s2]", sel.missing)
	}
}
