package uncertain

import (
	"bytes"
	"context"
	"sync"
	"testing"
)

func TestCaptureKeepsSourceFieldArraysAndMalformedResponses(t *testing.T) {
	ctx := WithContext(context.Background(), New())
	ctx, done := Begin(ctx)
	c := FromContext(ctx)
	c.Record("response", "m", Source{ID: "a"}, []byte(`{"vendor":1}`), "")
	c.Record("response", "m", Source{ID: "b"}, []byte(`{"vendor":1}`), "")
	c.Record("response", "m", Source{ID: "c"}, []byte(`{"vendor":`), "")
	done()
	s := c.Snapshot()
	if len(s.Entries) != 2 || len(s.Responses) != 3 {
		t.Fatalf("entries=%d responses=%d", len(s.Entries), len(s.Responses))
	}
	if !bytes.Equal(s.Responses[2].Value, []byte(`{"vendor":`)) {
		t.Fatalf("malformed response was not retained: %q", s.Responses[2].Value)
	}
}

func TestCaptureSnapshotIsDefensiveAndLateRecordsFreeze(t *testing.T) {
	c := New()
	ctx, done := Begin(WithContext(context.Background(), c))
	c.Record("response", "m", Source{ID: "a"}, []byte(`{"x":"before"}`), "")
	done()
	s := c.Snapshot()
	s.Entries[0].Value[0] = 'X'
	s.Responses[0].Value[0] = 'X'
	c.Record("response", "m", Source{ID: "late"}, []byte(`{"x":"late"}`), "")
	if got := c.Snapshot(); len(got.Entries) != 1 || got.Entries[0].Value[0] != '"' {
		t.Fatalf("published snapshot changed: %+v", got)
	}
	// Reusing the original context after publication is ignored.
	if _, end := Begin(ctx); end == nil {
		t.Fatal("begin should always return a completion function")
	}
}

func TestCaptureNestedAndConcurrentReaders(t *testing.T) {
	c := New()
	ctx, done := Begin(WithContext(context.Background(), c))
	nestedCtx, nestedDone := Begin(ctx)
	c.Record("reference", "anchor", Source{ID: "a"}, []byte(`{"hash":"0x1"}`), "")
	nestedDone()
	done()
	if nestedCtx == nil || len(c.Snapshot().Entries) != 1 {
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
	c.Record("response", "first", Source{ID: "a"}, []byte(`{"x":1}`), "")
	second, secondDone := Begin(ctx)
	if isolated := FromContext(second); isolated != nil {
		isolated.Record("response", "second", Source{ID: "b"}, []byte(`{"x":2}`), "")
	}
	secondDone()
	done()
	if got := c.Snapshot(); len(got.Responses) != 1 || got.Responses[0].Method != "first" {
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
	c.Record("response", "m", Source{ID: "a"}, large, "")
	done()
	s := c.Snapshot()
	if s.Bytes > s.Limit || !s.Truncated || s.Dropped == 0 {
		t.Fatalf("budget metadata invalid: bytes=%d limit=%d truncated=%v dropped=%d", s.Bytes, s.Limit, s.Truncated, s.Dropped)
	}
	_ = ctx
}
