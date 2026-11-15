package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
)

func TestCompilePlanReferenceDefaultValidation(t *testing.T) {
	base := simulateV1Spec()
	for _, tc := range []struct {
		name  string
		ref   ReferenceSpec
		valid bool
	}{
		{"hash", ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, DefaultTag: "latest"}, true},
		{"height", ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{1}, DefaultTag: "safe"}, true},
		{"none", ReferenceSpec{Kind: ReferenceShapeNone, DefaultTag: "latest"}, false},
		{"fields", ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"from"}, DefaultTag: "latest"}, false},
		{"multiple", ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{0, 1}, DefaultTag: "latest"}, false},
		{"negative", ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{-1}, DefaultTag: "latest"}, false},
		{"invalid tag", ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, DefaultTag: "0x1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			s.Reference = tc.ref
			_, err := CompilePlan(s)
			if (err == nil) != tc.valid {
				t.Fatalf("CompilePlan error = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestSimulateV1ReferenceForms(t *testing.T) {
	plan, err := CompilePlan(simulateV1Spec())
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"blockStateCalls": []any{map[string]any{}}}
	hash := "0x" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name        string
		params      []any
		valid, need bool
	}{
		{"omitted", []any{payload}, true, true},
		{"null", []any{payload, nil}, true, true},
		{"safe", []any{payload, "safe"}, true, true},
		{"pending", []any{payload, "pending"}, true, false},
		{"number", []any{payload, "0x10"}, true, false},
		{"hash", []any{payload, hash}, true, false},
		{"raw payload", []any{json.RawMessage(`{"blockStateCalls":[]}`), "latest"}, true, true},
		{"missing payload", nil, false, false},
		{"missing blocks", []any{map[string]any{}, "latest"}, false, true},
		{"null blocks", []any{map[string]any{"blockStateCalls": nil}, "latest"}, false, true},
		{"byte slice is not a JSON array", []any{map[string]any{"blockStateCalls": []byte{1}}, "latest"}, false, true},
		{"duplicate keys", []any{json.RawMessage(`{"blockStateCalls":[],"blockStateCalls":[]}`), "latest"}, false, true},
		{"object reference", []any{payload, map[string]any{"blockNumber": "latest"}}, false, false},
		{"bad reference", []any{payload, "banana"}, false, false},
		{"extra argument", []any{payload, "latest", true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := rpc.Request{Method: MethodSimulateV1, Params: tc.params}
			prepared, err := plan.Prepare(context.Background(), plan.ApplyReferenceDefault(req))
			if (err == nil) != tc.valid || (err != nil && !errors.Is(err, rpc.ErrInvalidRequest)) {
				t.Fatalf("validation error = %v, valid = %v", err, tc.valid)
			}
			_, need := plan.ReferenceRequirement(prepared)
			if !tc.valid {
				return
			}
			if need != tc.need {
				t.Fatalf("reference needed = %v, want %v", need, tc.need)
			}
		})
	}
	req := rpc.Request{Method: MethodSimulateV1, Params: []any{payload}}
	ref := rpc.ReferenceEvidence{Hash: hash, Height: 16, Status: rpc.Corroborated}
	prepared, pin, err := plan.PrepareWithReference(req, ref, true)
	if err != nil || pin != hash || !reflect.DeepEqual(prepared.Params, []any{payload, hash}) {
		t.Fatalf("prepared = %#v, pin %q, err %v", prepared, pin, err)
	}
	if len(req.Params) != 1 {
		t.Fatalf("input changed: %#v", req.Params)
	}
}

func TestSimulateV1NormalizationOfficialResult(t *testing.T) {
	data, err := fs.ReadFile(spec.VectorsFS(), "vectors/eth_simulateV1/ethSimulate-empty.io")
	if err != nil {
		t.Fatal(err)
	}
	var result json.RawMessage
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("<< ")) {
			var response struct {
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(line[3:], &response); err != nil {
				t.Fatal(err)
			}
			result = response.Result
			break
		}
	}
	if len(result) == 0 {
		t.Fatal("official vector has no result")
	}
	encoded, err := encodeSimulateV1(result)
	if err != nil {
		t.Fatalf("official result: %v", err)
	}
	key, err := normalizeSimulateV1(result)
	if err != nil || key != string(encoded) {
		t.Fatalf("key/encoding disagree: %v", err)
	}
	encodedAgain, err := encodeSimulateV1(encoded)
	if err != nil || !bytes.Equal(encoded, encodedAgain) {
		t.Fatalf("canonical result not stable: %v", err)
	}
	for _, invalid := range []string{`null`, `{}`, `[{}]`, `[{"calls":[],"calls":[]}]`, `not json`} {
		if _, err := normalizeSimulateV1(json.RawMessage(invalid)); err == nil {
			t.Errorf("accepted invalid result %s", invalid)
		}
	}
}

func TestSimulateV1RejectsInvalidEIP1898Object(t *testing.T) {
	plan, err := CompilePlan(simulateV1Spec())
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"blockStateCalls": []any{}}
	for _, ref := range []map[string]any{
		{"blockNumber": "latest"},
		{"blockHash": "0x1"},
		{"blockNumber": "safe", "requireCanonical": true},
	} {
		req := rpc.Request{Method: MethodSimulateV1, Params: []any{payload, ref}}
		if _, err := plan.Prepare(context.Background(), req); !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Errorf("reference %#v: error = %v", ref, err)
		}
	}
}

func TestSimulateV1PreparationConsumesAllOfficialRequests(t *testing.T) {
	plan, err := CompilePlan(simulateV1Spec())
	if err != nil {
		t.Fatal(err)
	}
	files := spec.VectorsFS()
	count := 0
	if err := fs.WalkDir(files, "vectors/eth_simulateV1", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(files, name)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			var request struct {
				Params []json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(vector.Request, &request); err != nil {
				return err
			}
			params := make([]any, len(request.Params))
			for i := range request.Params {
				params[i] = request.Params[i]
			}
			prepared, err := plan.Prepare(context.Background(), plan.ApplyReferenceDefault(rpc.Request{Method: MethodSimulateV1, Params: params}))
			if err != nil {
				t.Fatalf("%s rejected: %v", vector.Identity(), err)
			}
			if prepared.Method != MethodSimulateV1 || len(prepared.Params) != 2 {
				t.Fatalf("%s: %+v", vector.Identity(), prepared)
			}
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 95 {
		t.Fatalf("prepared %d requests, want 95", count)
	}
}
