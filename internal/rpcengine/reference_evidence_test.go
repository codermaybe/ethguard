package rpcengine

import (
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestReferenceEvidenceForwardedAdmission(t *testing.T) {
	member := rpc.SourceRef{ID: "node", Name: "node", URL: "http://node"}
	rule := Corroboration{Mode: rpc.CorroborationForwarded}
	for _, tc := range []struct {
		name    string
		purpose ReferencePurpose
		change  func(*Basis, *rpc.ReferenceEvidence)
		want    error
	}{
		{name: "genesis"},
		{name: "standalone", purpose: ReferencePurposeStandaloneAnchor, want: rpc.ErrGuaranteeNotMet},
		{name: "majority", change: func(b *Basis, _ *rpc.ReferenceEvidence) {
			b.Corroboration = Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
		}, want: rpc.ErrGuaranteeNotMet},
		{name: "multiple-members", change: func(b *Basis, _ *rpc.ReferenceEvidence) { b.Members = append(b.Members, rpc.SourceRef{ID: "other"}) }, want: rpc.ErrGuaranteeNotMet},
		{name: "foreign-basis", change: func(b *Basis, _ *rpc.ReferenceEvidence) { b.Members[0].ID = "other" }, want: rpc.ErrGuaranteeNotMet},
		{name: "foreign-support", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Support[0].URL = "http://other" }, want: rpc.ErrGuaranteeNotMet},
		{name: "foreign-participant", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Participants[0].Name = "other" }, want: rpc.ErrGuaranteeNotMet},
		{name: "missing-support", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Support = nil }, want: rpc.ErrGuaranteeNotMet},
		{name: "rejected-source", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Reject = []rpc.SourceRef{member} }, want: rpc.ErrGuaranteeNotMet},
		{name: "invalid-floor", change: func(b *Basis, _ *rpc.ReferenceEvidence) { b.Corroboration.Floor = 2 }, want: rpc.ErrGuaranteeNotMet},
		{name: "wrong-adjudication-basis", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Adjudication.Basis = 2 }, want: rpc.ErrGuaranteeNotMet},
		{name: "wrong-required", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Adjudication.Required = 2 }, want: rpc.ErrGuaranteeNotMet},
		{name: "unaccepted", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Adjudication.Reason = rpc.ReasonGuaranteeNotMet }, want: rpc.ErrGuaranteeNotMet},
		{name: "selected", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Status = rpc.Selected }, want: rpc.ErrGuaranteeNotMet},
		{name: "claimed-corroboration", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Status = rpc.Corroborated }, want: rpc.ErrGuaranteeNotMet},
		{name: "wrong-tag", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Tag = "safe" }, want: rpc.ErrReferenceRequired},
		{name: "missing-hash", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Hash = "" }, want: rpc.ErrMalformedResponse},
		{name: "short-hash", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Hash = "0xabc" }, want: rpc.ErrMalformedResponse},
		{name: "invalid-hex", change: func(_ *Basis, r *rpc.ReferenceEvidence) { r.Hash = "0x" + strings.Repeat("z", 64) }, want: rpc.ErrMalformedResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			basis := FreezeBasis([]rpc.SourceRef{member}, rule)
			ref := rpc.ReferenceEvidence{
				Hash: "0x" + strings.Repeat("ab", 32), Height: 0, Status: rpc.Forwarded,
				Adjudication: rpc.Adjudication{Basis: 1, Required: 1, Reason: rpc.ReasonAccepted},
				Participants: []rpc.SourceRef{member}, Support: []rpc.SourceRef{member},
			}
			if tc.change != nil {
				tc.change(&basis, &ref)
			}
			err := ValidateReferenceEvidence(tc.purpose, basis, "latest", ref)
			if !errors.Is(err, tc.want) {
				t.Fatalf("admission error = %v, want %v", err, tc.want)
			}
		})
	}
}
