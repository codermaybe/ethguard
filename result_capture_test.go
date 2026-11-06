package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func captureResult() CallResult {
	return CallResult{
		Decision: &rpc.Decision{
			Status:    rpc.Corroborated,
			Value:     json.RawMessage(`"0x1"`),
			Reference: &rpc.ReferenceEvidence{Hash: "0xabc", Status: rpc.Corroborated},
			Support:   []rpc.SourceRef{{Name: "a"}},
		},
		Uncertain: &UncertainResult{Raw: []UncertainResponse{{Stage: "response", Value: json.RawMessage(`"x"`)}}, Bytes: 4, Limit: 16},
	}
}

func TestResultCapturePublishesOnceAndIsolatesCopies(t *testing.T) {
	ctx, capture := WithResult(context.Background())
	if captureFromContext(ctx) != capture {
		t.Fatal("the capture did not ride the context")
	}
	// Before publication: zero result, false, nil — no waiting, no faked
	// completed call.
	result, published, err := capture.Result()
	if published || err != nil || result.Decision != nil || result.Uncertain != nil {
		t.Fatalf("unpublished read = %+v, %v, %v", result, published, err)
	}
	original := captureResult()
	if !capture.bind() {
		t.Fatal("bind failed")
	}
	capture.publish(original)
	original.Decision.Support[0].Name = "mutated-after-publish"

	first, publishedFirst, err := capture.Result()
	if !publishedFirst || err != nil {
		t.Fatalf("published read = %v, %v", publishedFirst, err)
	}
	if !first.IsCorroborated() || string(first.Decision.Value) != `"0x1"` {
		t.Fatalf("published decision = %+v", first.Decision)
	}
	if first.Uncertain == nil || first.Uncertain.Limit != 16 || len(first.Uncertain.Raw) != 1 || string(first.Uncertain.Raw[0].Value) != `"x"` {
		t.Fatalf("uncertain material lost or emptied by the clone: %+v", first.Uncertain)
	}
	// A delivered copy is isolated: mutating it cannot reach the capture or
	// a second Result copy.
	first.Decision.Support[0].Name = "caller-mutation"
	first.Decision.Value[0] = '"'
	first.Uncertain.Bytes = 999
	second, _, _ := capture.Result()
	if second.Decision.Support[0].Name != "a" || string(second.Decision.Value) != `"0x1"` || second.Uncertain.Bytes != 4 {
		t.Fatalf("capture material leaked into a second read: %+v", second)
	}
	if original.Decision.Support[0].Name != "mutated-after-publish" {
		t.Fatal("publish did not clone the delivered snapshot")
	}
}

func TestResultCaptureReuseIsPermanentAndDetected(t *testing.T) {
	_, capture := WithResult(context.Background())
	if !capture.bind() {
		t.Fatal("first bind must succeed")
	}
	capture.publish(captureResult())
	// A second independent call — even after completion — invalidates.
	if capture.bind() {
		t.Fatal("second bind must be refused")
	}
	result, published, err := capture.Result()
	if published || !errors.Is(err, ErrCaptureReused) || result.Decision != nil {
		t.Fatalf("reused read = %+v, %v, %v", result, published, err)
	}
	// The refusal is permanent, including after an abort attempt.
	capture.abort()
	if _, published, err := capture.Result(); published || !errors.Is(err, ErrCaptureReused) {
		t.Fatalf("reuse must win over an abnormal exit: %v %v", published, err)
	}
}

func TestResultCaptureRunningCallStaysReadable(t *testing.T) {
	_, capture := WithResult(context.Background())
	if !capture.bind() {
		t.Fatal("bind failed")
	}
	// A still-running capture has nothing published; Result never waits.
	if result, published, err := capture.Result(); published || err != nil || result.Decision != nil {
		t.Fatalf("running read = %+v, %v, %v", result, published, err)
	}
	capture.abort()
	if _, published, err := capture.Result(); published || !errors.Is(err, ErrCaptureAborted) {
		t.Fatalf("aborted read = %v, %v", published, err)
	}
}

func TestResultCaptureConcurrentBindOnOneHandle(t *testing.T) {
	_, capture := WithResult(context.Background())
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if capture.bind() {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if admitted != 1 {
		t.Fatalf("concurrent binds admitted = %d, want exactly one", admitted)
	}
	if _, published, err := capture.Result(); published || !errors.Is(err, ErrCaptureReused) {
		t.Fatalf("losers must see permanent reuse: %v %v", published, err)
	}
}

func TestResultCaptureNilAndZeroValueReadClean(t *testing.T) {
	var zero *ResultCapture
	if result, published, err := zero.Result(); published || err != nil || result.Decision != nil {
		t.Fatalf("nil capture read = %+v, %v, %v", result, published, err)
	}
	var byValue ResultCapture
	if result, published, err := byValue.Result(); published || err != nil || result.Decision != nil {
		t.Fatalf("zero capture read = %+v, %v, %v", result, published, err)
	}
	// Zero-value publish is refused; the state machine cannot be reached
	// from a zero value by accident.
	byValue.publish(captureResult())
	if _, published, _ := byValue.Result(); published {
		t.Fatal("a zero-value capture must not publish")
	}
}

func TestCallResultMutualExclusionAndPredicates(t *testing.T) {
	var empty CallResult
	if empty.IsCorroborated() || empty.IsWriteAccepted() {
		t.Fatal("zero result claims nothing")
	}
	read := CallResult{Decision: &rpc.Decision{Status: rpc.Corroborated}}
	if !read.IsCorroborated() || read.IsWriteAccepted() {
		t.Fatal("corroborated read predicate mismatch")
	}
	// IsCorroborated does not consult Err: the two-layer fact split.
	failedRead := CallResult{Decision: &rpc.Decision{Status: rpc.Corroborated}, Err: ErrConflict}
	if !failedRead.IsCorroborated() {
		t.Fatal("a corroborated observation stays corroborated with a business error")
	}
	selected := CallResult{Decision: &rpc.Decision{Status: rpc.Selected}}
	if selected.IsCorroborated() {
		t.Fatal("Selected is not Corroborated")
	}
	write := CallResult{WriteDecision: &rpc.WriteDecision{Status: rpc.WriteAccepted, Hash: "0xabc"}}
	if !write.IsWriteAccepted() || write.IsCorroborated() {
		t.Fatal("accepted write predicate mismatch")
	}
	noHash := CallResult{WriteDecision: &rpc.WriteDecision{Status: rpc.WriteAccepted}}
	if noHash.IsWriteAccepted() {
		t.Fatal("an accepted status without a hash is not a concrete write acceptance")
	}
}

func TestWithSourceEvidenceValidatesBudget(t *testing.T) {
	ctx := context.Background()
	if got, err := WithSourceEvidence(ctx, 0); !errors.Is(err, ErrInvalidRequest) || got != ctx {
		t.Fatalf("zero budget: ctx unchanged=%v err=%v", got == ctx, err)
	}
	if got, err := WithSourceEvidence(ctx, -5); !errors.Is(err, ErrInvalidRequest) || got != ctx {
		t.Fatalf("negative budget: ctx unchanged=%v err=%v", got == ctx, err)
	}
	configured, err := WithSourceEvidence(ctx, 1024)
	if err != nil {
		t.Fatalf("valid budget: %v", err)
	}
	// The last configuration on a derivation chain wins.
	final, err := WithSourceEvidence(configured, 2048)
	if err != nil {
		t.Fatalf("reconfigured: %v", err)
	}
	if final == configured {
		t.Fatal("reconfiguration must derive a new context")
	}
}
