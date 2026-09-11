package uncertain

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"ethguard/rpc"
)

func TestCaptureKeepsSourceFieldArraysAndMalformedResponses(t *testing.T) {
	ctx := WithContext(context.Background(), New())
	ctx, done := Begin(ctx)
	c := FromContext(ctx)
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"vendor":1}`), "")
	c.Record("response", "m", rpc.SourceRef{ID: "b"}, []byte(`{"vendor":1}`), "")
	c.Record("response", "m", rpc.SourceRef{ID: "c"}, []byte(`{"vendor":`), "")
	done()
	s := c.Snapshot()
	if len(s.Fields) != 2 || len(s.Raw) != 3 {
		t.Fatalf("fields=%d raw=%d", len(s.Fields), len(s.Raw))
	}
	if !bytes.Equal(s.Raw[2].Value, []byte(`{"vendor":`)) {
		t.Fatalf("malformed response was not retained: %q", s.Raw[2].Value)
	}
}

func TestCaptureSnapshotIsDefensiveAndLateRecordsFreeze(t *testing.T) {
	c := New()
	ctx, done := Begin(WithContext(context.Background(), c))
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, []byte(`{"x":"before"}`), "")
	done()
	s := c.Snapshot()
	s.Fields[0].Value[0] = 'X'
	s.Raw[0].Value[0] = 'X'
	c.Record("response", "m", rpc.SourceRef{ID: "late"}, []byte(`{"x":"late"}`), "")
	if got := c.Snapshot(); len(got.Fields) != 1 || got.Fields[0].Value[0] != '"' {
		t.Fatalf("published snapshot changed: %+v", got)
	}
	if _, end := Begin(ctx); end == nil {
		t.Fatal("begin should always return a completion function")
	}
}

func TestCaptureNestedAndConcurrentReaders(t *testing.T) {
	c := New()
	ctx, done := Begin(WithContext(context.Background(), c))
	nestedCtx, nestedDone := Begin(ctx)
	c.Record("reference", "anchor", rpc.SourceRef{ID: "a"}, []byte(`{"hash":"0x1"}`), "")
	nestedDone()
	done()
	if nestedCtx == nil || len(c.Snapshot().Fields) != 1 {
		t.Fatal("nested record missing")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Snapshot()
		}()
	}
	wg.Wait()
}

func TestCaptureReuseDoesNotMergeTopLevelCalls(t *testing.T) {
	c := New()
	ctx := WithContext(context.Background(), c)
	first, done := Begin(ctx)
	c.Record("response", "first", rpc.SourceRef{ID: "a"}, []byte(`{"x":1}`), "")
	second, secondDone := Begin(ctx)
	if isolated := FromContext(second); isolated != nil {
		isolated.Record("response", "second", rpc.SourceRef{ID: "b"}, []byte(`{"x":2}`), "")
	}
	secondDone()
	done()
	if got := c.Snapshot(); len(got.Raw) != 1 || got.Raw[0].Method != "first" {
		t.Fatalf("reused capture merged records: %+v", got)
	}
	if first == second {
		t.Fatal("reused top-level context should be isolated")
	}
}

func TestCaptureBudgetIsBoundedDuringRecord(t *testing.T) {
	c := New()
	ctx, done := Begin(WithContext(context.Background(), c))
	large := bytes.Repeat([]byte{'x'}, 2<<20)
	c.Record("response", "m", rpc.SourceRef{ID: "a"}, large, "")
	done()
	s := c.Snapshot()
	if s.Bytes > s.Limit || !s.Truncated || s.Dropped == 0 {
		t.Fatalf("budget metadata invalid: bytes=%d limit=%d truncated=%v dropped=%d", s.Bytes, s.Limit, s.Truncated, s.Dropped)
	}
	_ = ctx
}
