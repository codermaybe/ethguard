package rpcengine

import (
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestReferenceDefaultPositionalBehavior(t *testing.T) {
	for _, shape := range []ReferenceShape{ReferenceShapeHashAt, ReferenceShapeHeightAt} {
		t.Run(string(shape), func(t *testing.T) {
			plan := ReadPlan{Reference: ReferenceSpec{Kind: shape, Positions: []int{1}, DefaultTag: "latest"}}
			for _, tc := range []struct {
				name        string
				input, want []any
			}{
				{"missing", []any{"payload"}, []any{"payload", "latest"}},
				{"nil", []any{"payload", nil}, []any{"payload", "latest"}},
				{"explicit", []any{"payload", "safe"}, []any{"payload", "safe"}},
				{"missing required payload", nil, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					input := append([]any(nil), tc.input...)
					req := rpc.Request{Method: "example", Params: tc.input}
					got := plan.ApplyReferenceDefault(req)
					if got.Method != req.Method || !reflect.DeepEqual(got.Params, tc.want) {
						t.Fatalf("default = %#v, want %#v", got, tc.want)
					}
					if !reflect.DeepEqual(req.Params, input) {
						t.Fatalf("mutated caller params: %#v", req.Params)
					}
					if !reflect.DeepEqual(plan.ApplyReferenceDefault(got), got) {
						t.Fatal("defaulting is not idempotent")
					}
					if len(tc.input) > 0 && (len(tc.input) == 1 || tc.input[1] == nil) {
						got.Params[0] = "changed"
						if req.Params[0] != "payload" {
							t.Fatal("defaulted params alias input")
						}
					}
				})
			}
			req := rpc.Request{Method: "example", Params: []any{"payload"}}
			need, ok := plan.ReferenceRequirement(req)
			if !ok || need.Kind != rpc.ReferenceLatest {
				t.Fatalf("requirement = %#v, %v; want latest", need, ok)
			}
			prepared, pin, err := plan.PrepareWithReference(req, planEvidence("latest"), true, planTestBasis())
			want := "0xabc"
			if shape == ReferenceShapeHeightAt {
				want = "0x10"
			}
			if err != nil || pin != want || !reflect.DeepEqual(prepared.Params, []any{"payload", want}) {
				t.Fatalf("prepared = %#v pin %q err %v", prepared, pin, err)
			}
		})
	}
}

func TestReferenceDefaultIgnoresInapplicableDeclarations(t *testing.T) {
	req := rpc.Request{Method: "example", Params: []any{"payload", nil}}
	for _, ref := range []ReferenceSpec{
		{Kind: ReferenceShapeHashAt, Positions: []int{1}},
		{Kind: ReferenceShapeNone, Positions: []int{1}, DefaultTag: "latest"},
		{Kind: ReferenceShapeHeightFieldsAt, Positions: []int{1}, DefaultTag: "latest"},
		{Kind: ReferenceShapeHashAt, Positions: []int{0, 1}, DefaultTag: "latest"},
		{Kind: ReferenceShapeHashAt, Positions: []int{-1}, DefaultTag: "latest"},
		{Kind: ReferenceShapeHashAt, Positions: []int{3}, DefaultTag: "latest"},
	} {
		got := (ReadPlan{Reference: ref}).ApplyReferenceDefault(req)
		if !reflect.DeepEqual(got, req) {
			t.Errorf("reference %#v changed request: %#v", ref, got)
		}
	}
}
