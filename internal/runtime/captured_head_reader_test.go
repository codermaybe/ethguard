package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/rpc"
)

type captureRecord struct {
	stage   string
	method  string
	source  rpc.SourceRef
	value   []byte
	errText string
}

type recordingCapture struct {
	mu      sync.Mutex
	records []captureRecord
}

func (c *recordingCapture) Record(stage, method string, source rpc.SourceRef, value []byte, errText string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, captureRecord{
		stage:   stage,
		method:  method,
		source:  source,
		value:   append([]byte(nil), value...),
		errText: errText,
	})
}

func (c *recordingCapture) snapshot() []captureRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]captureRecord(nil), c.records...)
}

type stubHeadReader struct {
	readObservations    []reference.HeadObservation
	readErr             error
	confirmObservations []reference.HeadObservation
	confirmErr          error
	confirmedHeight     uint64
	confirmedIDs        []string
}

func (r *stubHeadReader) Read(context.Context, string, time.Duration) ([]reference.HeadObservation, error) {
	return r.readObservations, r.readErr
}

func (r *stubHeadReader) Confirm(_ context.Context, height uint64, sourceIDs []string, _ time.Duration) ([]reference.HeadObservation, error) {
	r.confirmedHeight = height
	r.confirmedIDs = append([]string(nil), sourceIDs...)
	return r.confirmObservations, r.confirmErr
}

// The finalized reader wrapper records both rounds and their collect errors
// without touching the reference algorithm or the reader signature.
func TestCapturedHeadReaderRecordsBothRoundsAndCollectErrors(t *testing.T) {
	collectErr := errors.New("collect: deadline")
	inner := &stubHeadReader{
		readObservations: []reference.HeadObservation{
			{Source: rpc.SourceRef{ID: "a", Name: "a"}, Value: []byte(`{"number":"0x64"}`)},
			{Source: rpc.SourceRef{ID: "b", Name: "b"}, Err: errors.New("timeout")},
		},
		readErr: collectErr,
		confirmObservations: []reference.HeadObservation{
			{Source: rpc.SourceRef{ID: "c", Name: "c"}, Value: []byte(`{"number":"0x64"}`)},
		},
	}
	capture := &recordingCapture{}
	invocation := rpc.NewInvocation("capture-reader", rpc.FreezeBasis(nil, rpc.Corroboration{}),
		rpc.WithInvocationCapture(capture))
	reader := capturedHeadReader{inner: inner, invocation: invocation}

	if _, err := reader.Read(context.Background(), "finalized", time.Second); !errors.Is(err, collectErr) {
		t.Fatalf("Read err = %v, want the inner collect error", err)
	}
	if _, err := reader.Confirm(context.Background(), 100, []string{"c"}, time.Second); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if inner.confirmedHeight != 100 || len(inner.confirmedIDs) != 1 || inner.confirmedIDs[0] != "c" {
		t.Fatalf("Confirm received (%d, %v), want the requested height and members", inner.confirmedHeight, inner.confirmedIDs)
	}

	records := capture.snapshot()
	if len(records) != 4 {
		t.Fatalf("recorded %d events, want 3 responses + 1 collect", len(records))
	}
	responses := 0
	collects := 0
	for _, record := range records {
		if record.method != anchorMethodName {
			t.Fatalf("record method = %q, want the anchor label", record.method)
		}
		switch record.stage {
		case "response":
			responses++
		case "collect":
			collects++
			if record.errText != collectErr.Error() {
				t.Fatalf("collect record = %q, want the inner collect error", record.errText)
			}
		default:
			t.Fatalf("unexpected stage %q", record.stage)
		}
	}
	if responses != 3 || collects != 1 {
		t.Fatalf("recorded %d responses and %d collect events, want 3 and 1", responses, collects)
	}
	if string(records[0].value) != `{"number":"0x64"}` || records[1].errText != "timeout" {
		t.Fatalf("response records lost their value or error: %+v", records[:2])
	}
}
