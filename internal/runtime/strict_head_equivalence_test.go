package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The strict head establishment has exactly two callers: the facade's
// resolver service (direct calls, no invocation) and the establishment stage
// (reference rounds inside a business invocation). Both drive one engine, so
// their evidence must agree on members, thresholds, and error assembly.
// These tests pin that equality across the scenarios where two copies used
// to diverge: partial failures, collection failures, malformed bodies, encode
// failures, and the corroboration floors.
const (
	equivTimeout = 2 * time.Second
	equivHead    = "0xabc"
	equivHeight  = 100
)

type headHandler = rpctest.Handler

func equivFleet(t *testing.T, serve ...headHandler) []*rpctest.Node {
	t.Helper()
	common := func(rpctest.Call) (any, error) {
		return map[string]any{
			"number": fmt.Sprintf("0x%x", equivHeight),
			"hash":   equivHead,
		}, nil
	}
	if len(serve) == 0 {
		serve = []headHandler{common, common, common}
	}
	nodes := make([]*rpctest.Node, len(serve))
	for i, handler := range serve {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		if handler == nil {
			handler = common
		}
		node.Handle("eth_getBlockByNumber", handler)
		nodes[i] = node
	}
	return nodes
}

func equivPool(t *testing.T, nodes []*rpctest.Node) (*pool.Pool, []*pool.Source) {
	t.Helper()
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	t.Cleanup(p.Close)
	sources := make([]*pool.Source, 0, len(nodes))
	for i, node := range nodes {
		source, err := p.Join(fmt.Sprintf("node-%d", i), node.URL())
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source)
	}
	return p, sources
}

// establishService runs the strict round the facade resolver service way: a
// direct call over the pool's current voting members.
func establishService(t *testing.T, nodes []*rpctest.Node, shared *rpc.CorroborationShared, opts []rpc.StrictOption, tag string) (rpc.ReferenceEvidence, error) {
	t.Helper()
	pool, _ := equivPool(t, nodes)
	service := NewStrictHeadService(StrictHeadConfig{
		Pool:           pool,
		Recorder:       observe.Nop,
		CollectTimeout: equivTimeout,
		Corroboration:  shared,
		StrictOptions:  opts,
	})
	if tag == "" || tag == string(rpc.ReferenceLatest) {
		return service.Reference(context.Background())
	}
	return service.ReferenceForTag(context.Background(), tag)
}

// establishStage runs the same round the establishment stage way: reference
// rounds on the invocation's frozen basis inside a business-ready context.
func establishStage(t *testing.T, nodes []*rpctest.Node, shared *rpc.CorroborationShared, opts []rpc.StrictOption, tag string) (rpc.ReferenceEvidence, error) {
	t.Helper()
	invPool, sources := equivPool(t, nodes)
	stage := NewReferenceStage(ReferenceStageConfig{
		Pool:          invPool,
		Timeout:       equivTimeout,
		MaxLag:        32,
		Recorder:      observe.Nop,
		StrictOptions: opts,
	})
	refs := make([]rpc.SourceRef, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, rpc.SourceRef{Name: source.Name(), URL: source.URL(), ID: source.ID()})
	}
	invocation := rpc.NewInvocation(rpc.NewInvocationID(), rpc.FreezeBasis(refs, shared.Snapshot()))
	ctx, capture := refcontext.New(context.Background())
	kind := rpc.ReferenceKind(tag)
	completed, err := stage.Establish(ctx, invocation, rpc.ReferenceRequest{Kind: kind})
	if err != nil {
		if published := capture.Reference(); published != nil {
			return *published, err
		}
		return rpc.ReferenceEvidence{}, err
	}
	ref, ok := completed.Reference()
	if !ok {
		t.Fatal("the established round left no reference on the invocation")
	}
	return ref, nil
}

// assertEvidenceAg mirror asserts that the service round and the stage round
// produce the same evidence shape for the same fleet: identity, thresholds,
// and fault assembly. ObservedAt is wall-clock by contract and compared for
// presence only.
func assertEvidenceAgree(t *testing.T, name string, service, stage rpc.ReferenceEvidence, serviceErr, stageErr error) {
	t.Helper()
	if (serviceErr == nil) != (stageErr == nil) {
		t.Fatalf("%s: establishment outcome diverged: service err %v, stage err %v", name, serviceErr, stageErr)
	}
	if serviceErr != nil && !sameErrorShape(serviceErr, stageErr) {
		t.Fatalf("%s: establishment errors diverged: service %v, stage %v", name, serviceErr, stageErr)
	}
	if service.Status != stage.Status {
		t.Fatalf("%s: status = %s vs %s", name, service.Status, stage.Status)
	}
	if service.Origin != stage.Origin {
		t.Fatalf("%s: origin = %q vs %q", name, service.Origin, stage.Origin)
	}
	if service.Tag != stage.Tag {
		t.Fatalf("%s: tag = %q vs %q", name, service.Tag, stage.Tag)
	}
	if service.Hash != stage.Hash || service.Height != stage.Height {
		t.Fatalf("%s: reference block = %s/%d vs %s/%d", name, service.Hash, service.Height, stage.Hash, stage.Height)
	}
	if !sameSourceSets(service.Participants, stage.Participants) {
		t.Fatalf("%s: participants = %v vs %v", name, service.Participants, stage.Participants)
	}
	if !sameSourceSets(service.Support, stage.Support) {
		t.Fatalf("%s: support = %v vs %v", name, service.Support, stage.Support)
	}
	if !sameSourceSets(service.Reject, stage.Reject) {
		t.Fatalf("%s: reject = %v vs %v", name, service.Reject, stage.Reject)
	}
	if !sameFaultTexts(service.Faults, stage.Faults) {
		t.Fatalf("%s: faults = %v vs %v", name, service.Faults, stage.Faults)
	}
	if !sameSourceFaults(service.SourceFaults, stage.SourceFaults) {
		t.Fatalf("%s: source faults = %v vs %v", name, service.SourceFaults, stage.SourceFaults)
	}
	if !sameFaultTexts(service.CollectionFaults, stage.CollectionFaults) {
		t.Fatalf("%s: collection faults = %v vs %v", name, service.CollectionFaults, stage.CollectionFaults)
	}
	if service.ObservedAt.IsZero() || stage.ObservedAt.IsZero() {
		t.Fatalf("%s: missing observation timestamp: %v vs %v", name, service.ObservedAt, stage.ObservedAt)
	}
}

func sameErrorShape(a, b error) bool { return a.Error() == b.Error() }

func sameSourceSets(a, b []rpc.SourceRef) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, ref := range a {
		seen[ref.ID]++
	}
	for _, ref := range b {
		if seen[ref.ID] == 0 {
			return false
		}
		seen[ref.ID]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func sameFaultTexts(a, b []error) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Error() != b[i].Error() {
			return false
		}
	}
	return true
}

func sameSourceFaults(a, b []rpc.SourceFault) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Source.ID != b[i].Source.ID {
			return false
		}
		if a[i].Err.Error() != b[i].Err.Error() {
			return false
		}
	}
	return true
}

func sharedCorroboration(t *testing.T, mode rpc.CorroborationMode, floor int) *rpc.CorroborationShared {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	return shared
}

// The core scenarios. Every case runs both callers over the same scripted
// fleet and requires one evidence shape.
func TestStrictHeadServiceAndStageEvidenceAgree(t *testing.T) {
	dissenting := func(rpctest.Call) (any, error) {
		return map[string]any{"number": "0x64", "hash": "0x999111"}, nil
	}
	refused := func(rpctest.Call) (any, error) { return nil, errors.New("connection refused") }
	// A structurally valid JSON body the canonical object encoder rejects:
	// a head shape with an unknown field picked up by the head codec's
	// stricter view. Both callers must classify it identically inside the
	// denominator.
	encodeFailure := func(rpctest.Call) (any, error) {
		return map[string]any{"number": "wrong", "hash": equivHead}, nil
	}
	scenarios := []struct {
		name  string
		serve []headHandler
		tag   string
		mode  rpc.CorroborationMode
		floor int
	}{
		{name: "unanimous-latest", mode: rpc.CorroborationMajority, floor: 2},
		{name: "one-provider-down", serve: []headHandler{nil, nil, refused}, mode: rpc.CorroborationMajority, floor: 2},
		{name: "one-dissenting-head", serve: []headHandler{nil, nil, dissenting}, mode: rpc.CorroborationMajority, floor: 2},
		{
			name:  "encode-failure-stays-in-denominator",
			serve: []headHandler{nil, nil, encodeFailure},
			mode:  rpc.CorroborationMajority, floor: 2,
		},
		{name: "unanimity-floor-with-one-failure", serve: []headHandler{nil, nil, refused}, mode: rpc.CorroborationUnanimity, floor: 3},
		{name: "safe-tag", tag: string(rpc.ReferenceSafe), mode: rpc.CorroborationMajority, floor: 2},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			nodes := equivFleet(t, scenario.serve...)
			shared := sharedCorroboration(t, scenario.mode, scenario.floor)
			tag := scenario.tag
			if tag == "" {
				tag = string(rpc.ReferenceLatest)
			}
			serviceRef, serviceErr := establishService(t, nodes, shared, nil, tag)
			stageRef, stageErr := establishStage(t, nodes, shared, nil, tag)
			assertEvidenceAgree(t, scenario.name, serviceRef, stageRef, serviceErr, stageErr)
		})
	}
}

// A whole-fleet collection failure keeps the pool's own sentinel on both
// callers, with the collection fault on the reference both publish.
func TestStrictHeadCollectionFailureKeepsSentinelOnBothChannels(t *testing.T) {
	nodes := equivFleet(t)
	for _, node := range nodes {
		node.Close() // the fan-out fails at the transport level on every member
	}
	shared := sharedCorroboration(t, rpc.CorroborationMajority, 2)
	serviceRef, serviceErr := establishService(t, nodes, shared, nil, string(rpc.ReferenceLatest))
	stageRef, stageErr := establishStage(t, nodes, shared, nil, string(rpc.ReferenceLatest))
	if !errors.Is(serviceErr, pool.ErrAllSourcesDown) || !errors.Is(stageErr, pool.ErrAllSourcesDown) {
		t.Fatalf("collection failures diverged: service %v, stage %v", serviceErr, stageErr)
	}
	assertEvidenceAgree(t, "collection-failure", serviceRef, stageRef, serviceErr, stageErr)
	if len(serviceRef.CollectionFaults) == 0 || len(stageRef.CollectionFaults) == 0 {
		t.Fatalf("collection faults = %v vs %v, want the collect fault on both", serviceRef.CollectionFaults, stageRef.CollectionFaults)
	}
}

// Every delivered block body failing the structural admission stage is a
// malformed-response failure on both channels, not a silent empty cluster.
func TestStrictHeadMalformedBodyAgreesOnBothChannels(t *testing.T) {
	// Deeply nested valid JSON the raw-data validator rejects.
	var bloat bytes.Buffer
	bloat.WriteString(`{"number":"0x64","hash":"` + equivHead + `","nest":[`)
	for i := 0; i < 4000; i++ {
		bloat.WriteByte('[')
	}
	for i := 0; i < 4000; i++ {
		bloat.WriteByte(']')
	}
	bloat.WriteString("]}")
	raw := json.RawMessage(bloat.Bytes())

	nodes := equivFleet(t)
	for _, node := range nodes {
		node.ReturnRaw("eth_getBlockByNumber", raw)
	}
	shared := sharedCorroboration(t, rpc.CorroborationMajority, 2)
	serviceRef, serviceErr := establishService(t, nodes, shared, nil, string(rpc.ReferenceLatest))
	stageRef, stageErr := establishStage(t, nodes, shared, nil, string(rpc.ReferenceLatest))
	assertEvidenceAgree(t, "malformed-body", serviceRef, stageRef, serviceErr, stageErr)
	if !errors.Is(serviceErr, rpc.ErrMalformedResponse) {
		t.Fatalf("service err = %v, want a malformed-response failure", serviceErr)
	}
}
