package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/ethobject/hexcompat"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

const MethodSimulateV1 = "eth_simulateV1"

func simulateV1Spec() MethodSpec {
	return MethodSpec{
		Method: MethodSimulateV1, Resolver: ResolverStrict,
		Normalize: normalizeSimulateV1, Encode: encodeSimulateV1,
		KeySource: rpcengine.RuleKeyCanonicalBytes, CompareKind: rpcengine.RuleCompareObject,
		Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true, DefaultTag: "latest"},
		Pending:   PendingSpec{Mode: PendingObserve, Pos: 1},
		Prepare:   prepareSimulateV1Request, ObservationProjectorID: ProjectorSimulateV1,
	}
}

func simulateV1Descriptor() MethodDescriptor {
	return MethodDescriptor{Name: MethodSimulateV1, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "CompilePlan", Adapter: "SimulateV1", Alignment: "v1.17.10", Notes: "Native execution with generated request/result models and official block-reference forms"}
}

// prepareSimulateV1Request takes one snapshot of user-controlled values before
// reference establishment. Only owned canonical JSON and decoded JSON values
// leave this hook, so the later wire freeze cannot invoke user marshalers again.
func prepareSimulateV1Request(_ context.Context, req rpc.Request) (rpc.Request, error) {
	invalid := func(err error) (rpc.Request, error) {
		return rpc.Request{}, fmt.Errorf("%w: %s: %v", rpc.ErrInvalidRequest, MethodSimulateV1, err)
	}
	if len(req.Params) != 2 {
		return invalid(fmt.Errorf("expected payload and base reference"))
	}
	frozen, _, err := rpcengine.FreezeParams(req.Params)
	if err != nil {
		return invalid(err)
	}
	payload, err := ethapi.NewSimulateV1Request(frozen[0].(json.RawMessage))
	if err != nil {
		return invalid(err)
	}
	payloadJSON, err := ethcodec.MarshalNoEscape(payload)
	if err != nil {
		return invalid(err)
	}
	baseJSON := frozen[1].(json.RawMessage)
	// Omitted, nil, typed-nil and explicit JSON null use the declared default;
	// null is never passed to the official five-branch reference constructor.
	if bytes.Equal(bytes.TrimSpace(baseJSON), []byte("null")) {
		baseJSON = json.RawMessage(`"latest"`)
	}
	base, err := ethapi.NewSimulateBlockReference(baseJSON)
	if err != nil {
		return invalid(err)
	}
	baseJSON, err = ethcodec.MarshalNoEscape(base)
	if err != nil {
		return invalid(err)
	}
	var reference any
	decoder := json.NewDecoder(bytes.NewReader(baseJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&reference); err != nil {
		return invalid(err)
	}
	return rpc.Request{Method: req.Method, Params: []any{payloadJSON, reference}}, nil
}

func encodeSimulateV1(raw json.RawMessage) (json.RawMessage, error) {
	candidate, err := hexcompat.SimulateResult(raw)
	if err != nil {
		return nil, fmt.Errorf("simulate result JSON: %w", err)
	}
	value, err := ethapi.NewSimulateV1Result(candidate)
	if err != nil {
		return nil, err
	}
	return ethcodec.MarshalNoEscape(value)
}

func normalizeSimulateV1(raw json.RawMessage) (string, error) {
	canonical, err := encodeSimulateV1(raw)
	return string(canonical), err
}
