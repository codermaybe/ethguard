package rpc

import (
	"errors"
	"testing"
)

func TestReferenceEvidenceCloneCopiesMutableEvidence(t *testing.T) {
	original := ReferenceEvidence{
		Participants: []SourceRef{{ID: "a"}},
		Support:      []SourceRef{{ID: "a"}},
		Reject:       []SourceRef{{ID: "b"}},
		Faults:       []error{errors.New("fault")},
	}
	clone := original.Clone()
	clone.Participants[0].ID = "changed"
	clone.Support[0].ID = "changed"
	clone.Reject[0].ID = "changed"
	clone.Faults[0] = errors.New("changed")
	if original.Participants[0].ID != "a" || original.Support[0].ID != "a" || original.Reject[0].ID != "b" || original.Faults[0].Error() != "fault" {
		t.Fatal("ReferenceEvidence.Clone did not isolate mutable slices")
	}
}
