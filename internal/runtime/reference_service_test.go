package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func composedReferenceService(t *testing.T, nodes []*rpctest.Node, finalized bool) *ReferenceService {
	t.Helper()
	in := Composition{Recorder: observe.Nop, CollectTimeout: equivTimeout}
	for i, node := range nodes {
		in.Upstreams = append(in.Upstreams, config.Upstream{ID: fmt.Sprintf("node-%d", i), Endpoint: node.URL()})
	}
	if finalized {
		in.Anchoring = &AnchoringStrategy{Finalized: true, MaxLag: DefaultMaxFinalizedLag}
	}
	bundle, err := Compose(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bundle.Pool.Close)
	return bundle.Reference
}

func TestComposedReferenceServicePresetTags(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			tag    string
			direct bool
		}{
			{name: "default", direct: true},
			{name: "empty"},
			{name: "latest", tag: "latest"},
			{name: "safe", tag: "safe"},
			{name: "finalized", tag: "finalized"},
		} {
			t.Run(fmt.Sprintf("finalized=%v/%s", finalized, tc.name), func(t *testing.T) {
				heights := map[string]uint64{"latest": 110, "safe": 100, "finalized": 90}
				bodies := make(map[string]json.RawMessage)
				hashes := make(map[string]string)
				for tag, height := range heights {
					bodies[tag], hashes[tag] = equivHeader(t, height, "0x1")
				}
				nodes := equivFleet(t)
				for _, node := range nodes {
					node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
						var tag string
						if err := json.Unmarshal(call.Params[0], &tag); err != nil {
							return nil, err
						}
						body, ok := bodies[tag]
						if !ok {
							return nil, fmt.Errorf("unexpected reference tag %q", tag)
						}
						return body, nil
					})
				}
				service := composedReferenceService(t, nodes, finalized)
				var ref rpc.ReferenceEvidence
				var err error
				if tc.direct {
					ref, err = service.Reference(context.Background())
				} else {
					ref, err = service.ReferenceForTag(context.Background(), tc.tag)
				}
				wireTag, evidenceTag, origin := tc.tag, tc.tag, "tagged-head"
				if wireTag == "" || wireTag == "latest" {
					wireTag, evidenceTag, origin = "latest", "", "strict-head"
				}
				if finalized && wireTag != "safe" {
					wireTag, origin = "finalized", "reported-finalized"
				}
				if err != nil || ref.Status != rpc.Corroborated || ref.Hash != hashes[wireTag] || ref.Height != heights[wireTag] {
					t.Fatalf("reference = %+v, error = %v; want %s at %d", ref, err, hashes[wireTag], heights[wireTag])
				}
				if ref.Tag != evidenceTag || ref.Origin != origin || ref.ObservedAt.IsZero() || ref.Adjudication.Basis != 3 || ref.Adjudication.Required != 2 || len(ref.Support) != 3 || len(ref.Reject) != 0 {
					t.Fatalf("reference evidence = %+v", ref)
				}
				for i, node := range nodes {
					reads := 0
					for _, call := range node.Calls() {
						if call.Method != "eth_getBlockByNumber" {
							continue
						}
						reads++
						if len(call.Params) != 2 || string(call.Params[0]) != fmt.Sprintf("%q", wireTag) || string(call.Params[1]) != "false" {
							t.Fatalf("node %d reference request = %+v, want [%q,false]", i, call, wireTag)
						}
					}
					if reads != 1 {
						t.Fatalf("node %d made %d reference reads, want 1", i, reads)
					}
				}
			})
		}
	}
}

// Safe uses the same strict algorithm in both presets, so its observable
// evidence distinguishes the client's Forwarded rule from the finalized
// preset's fixed majority floor. Neither may claim a corroborated anchor.
func TestComposedReferenceServiceSingleSourceRule(t *testing.T) {
	for _, tc := range []struct {
		name      string
		finalized bool
		status    rpc.Status
		required  int
		reason    rpc.DecisionReason
		err       error
	}{
		{name: "strict", status: rpc.Forwarded, required: 1, reason: rpc.ReasonGuaranteeNotMet, err: rpc.ErrGuaranteeNotMet},
		{name: "finalized", finalized: true, status: rpc.Unknown, required: 2, reason: rpc.ReasonInsufficientSupport, err: rpc.ErrInsufficientSupport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := equivFleet(t, nil)
			service := composedReferenceService(t, nodes, tc.finalized)
			ref, err := service.ReferenceForTag(context.Background(), "safe")
			if !errors.Is(err, tc.err) || ref.Status != tc.status || ref.Adjudication.Required != tc.required || ref.Adjudication.Reason != tc.reason {
				t.Fatalf("reference = %+v, error = %v; want status %s, required %d, reason %v, error %v", ref, err, tc.status, tc.required, tc.reason, tc.err)
			}
			if ref.Hash != "" || ref.Height != 0 || ref.Tag != "safe" || ref.Origin != "tagged-head" || ref.ObservedAt.IsZero() || ref.Adjudication.Basis != 1 || len(ref.Support) != 1 || len(ref.Reject) != 0 || len(ref.Faults) != 0 {
				t.Fatalf("single-source failure evidence = %+v", ref)
			}
		})
	}
}
