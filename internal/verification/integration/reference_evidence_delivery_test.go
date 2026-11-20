package ethguard_test

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The reference failure delivery contract across the two public facades. One
// establishment failure produces one evidence fact; the delivery surface —
// the explicit CallResult on the native Client, the captured result on the
// ethclient-shaped view, or no capture at all — must not change it. Both
// consumers read the same establishment stage's returned evidence, so each
// row below runs on its own identically scripted fleet and the shapes must
// match field for field (ObservedAt is wall-clock and compared for presence).

// failingAnchorFleet builds a fleet whose head endpoint delivers a body the
// anchor's codec rejects, so every establishment fails and keeps the round's
// faults.
func failingAnchorFleet(t *testing.T) (*ethguard.Client, []*rpctest.Node) {
	t.Helper()
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{})
	}
	return client, nodes
}

// referenceEvidenceShape reduces one evidence value to the fields the failure
// contract pins, so two deliveries compare without wall-clock noise.
type referenceEvidenceShape struct {
	tag              string
	status           rpc.Status
	origin           string
	participants     []string
	faults           []string
	supportCount     int
	rejectCount      int
	sourceFaultCount int
	collectFaultCnt  int
	observedAt       bool
}

func shapeOf(ref *rpc.ReferenceEvidence) referenceEvidenceShape {
	if ref == nil {
		return referenceEvidenceShape{status: rpc.Unknown}
	}
	shape := referenceEvidenceShape{
		tag:              ref.Tag,
		status:           ref.Status,
		origin:           ref.Origin,
		supportCount:     len(ref.Support),
		rejectCount:      len(ref.Reject),
		sourceFaultCount: len(ref.SourceFaults),
		collectFaultCnt:  len(ref.CollectionFaults),
		observedAt:       !ref.ObservedAt.IsZero(),
	}
	for _, participant := range ref.Participants {
		shape.participants = append(shape.participants, participant.Name)
	}
	sort.Strings(shape.participants)
	for _, fault := range ref.Faults {
		// The fault text embeds the endpoint the source answered on, which is
		// per-fleet incidental: normalize it so identical causes compare.
		shape.faults = append(shape.faults, endpointPattern.ReplaceAllString(fault.Error(), "<endpoint>"))
	}
	sort.Strings(shape.faults)
	return shape
}

var endpointPattern = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)

func TestReferenceFailureEvidenceMatchesAcrossBothFacades(t *testing.T) {
	// Native Client: the explicit result carries the failed establishment's
	// evidence on Decision.Reference.
	nativeClient, _ := failingAnchorFleet(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, callErr := nativeClient.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if callErr == nil {
		t.Fatal("expected the anchor failure to fail the read")
	}
	if result.Decision == nil || result.Decision.Reference == nil {
		t.Fatalf("native delivery dropped the failure evidence: %+v", result.Decision)
	}
	native := shapeOf(result.Decision.Reference)

	// EthClient-shaped view with a bound capture: the same failure reaches
	// the capture's delivered result.
	viewClient, _ := failingAnchorFleet(t)
	captureCtx, capture := ethguard.WithResult(ctx)
	if _, viewErr := viewClient.EthClient().BalanceAt(captureCtx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil); viewErr == nil {
		t.Fatal("expected the view's anchor failure to fail the read")
	}
	delivered, ok, _ := capture.Result()
	if !ok || delivered.Decision == nil || delivered.Decision.Reference == nil {
		t.Fatalf("view capture lost the failure evidence: ok=%v decision=%+v", ok, delivered.Decision)
	}
	captured := shapeOf(delivered.Decision.Reference)

	if native.tag != captured.tag || native.status != captured.status || native.origin != captured.origin ||
		native.observedAt != captured.observedAt ||
		native.supportCount != captured.supportCount || native.rejectCount != captured.rejectCount ||
		native.sourceFaultCount != captured.sourceFaultCount || native.collectFaultCnt != captured.collectFaultCnt ||
		!equalStrings(native.participants, captured.participants) || !equalStrings(native.faults, captured.faults) {
		t.Fatalf("failure evidence diverged across facades:\nnative  %+v\nview    %+v", native, captured)
	}
	if native.status != rpc.Unknown || !native.observedAt {
		t.Fatalf("failure evidence lost its contract shape: %+v", native)
	}
	if len(native.participants) != 3 {
		t.Fatalf("failure evidence lost the round's participants: %+v", native.participants)
	}
	if len(native.faults) == 0 || !strings.Contains(native.faults[0], "missing number or hash") {
		t.Fatalf("failure evidence lost the anchor cause: %+v", native.faults)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReferenceFailureBusinessErrorConsistentWithAndWithoutCapture pins the
// view's business failure: a bound capture adds a delivery surface, never a
// different failure. Two identically scripted fleets run the same view read
// with and without a capture, and the errors must match exactly.
func TestReferenceFailureBusinessErrorConsistentWithAndWithoutCapture(t *testing.T) {
	account := ethapi.Address("0x0000000000000000000000000000000000000dad")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bareClient, _ := failingAnchorFleet(t)
	_, bareErr := bareClient.EthClient().BalanceAt(ctx, account, nil)
	if bareErr == nil {
		t.Fatal("expected the anchor failure to fail the view read")
	}

	capturedClient, _ := failingAnchorFleet(t)
	captureCtx, capture := ethguard.WithResult(ctx)
	_, capturedErr := capturedClient.EthClient().BalanceAt(captureCtx, account, nil)
	if capturedErr == nil {
		t.Fatal("expected the anchor failure to fail the captured view read")
	}
	if bareErr.Error() != capturedErr.Error() {
		t.Fatalf("capture changed the business failure:\nbare     %v\ncaptured %v", bareErr, capturedErr)
	}
	delivered, ok, deliverErr := capture.Result()
	if !ok {
		t.Fatalf("captured view read published no result: %v", deliverErr)
	}
	if delivered.Decision == nil || delivered.Decision.Status != rpc.Unknown || delivered.Decision.Reference == nil {
		t.Fatalf("captured failure lost its decision facts: %+v", delivered.Decision)
	}
}
