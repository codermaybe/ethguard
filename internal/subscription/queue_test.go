package subscription

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func testLease(t *testing.T, arena *PayloadArena, value string) PayloadLease {
	t.Helper()
	lease, err := arena.Store(value, uint64(len(value)))
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestEventQueueDrainsProducerTerminalExactlyOnce(t *testing.T) {
	budget := NewMemoryBudget(0)
	arena := NewPayloadArena(budget)
	queue := NewEventQueue(budget)
	first := testLease(t, arena, "one")
	second := testLease(t, arena, "two")
	if err := queue.Push(Event{Header: first}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Push(Event{Header: second}); err != nil {
		t.Fatal(err)
	}
	terminal := errors.New("pump failed")
	_ = queue.Close(terminal)
	for _, want := range []string{"one", "two"} {
		event, err := queue.Next(context.Background())
		if err != nil || event.HeaderValue != want {
			t.Fatalf("Next = %+v, %v", event, err)
		}
	}
	if _, err := queue.Next(context.Background()); !errors.Is(err, terminal) {
		t.Fatalf("terminal = %v", err)
	}
	if _, err := queue.Next(context.Background()); !errors.Is(err, ErrNextAfterTerminal) {
		t.Fatalf("repeated terminal = %v", err)
	}
	if budget.Used() != 0 {
		t.Fatalf("payload or queue budget leaked: %d", budget.Used())
	}
}

func TestEventQueueCallerCloseDropsQueuedPayloadAndBudget(t *testing.T) {
	budget := NewMemoryBudget(0)
	arena := NewPayloadArena(budget)
	queue := NewEventQueue(budget)
	for _, value := range []string{"one", "two"} {
		if err := queue.Push(Event{Header: testLease(t, arena, value)}); err != nil {
			t.Fatal(err)
		}
	}
	_ = queue.Close()
	if _, err := queue.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Next = %v", err)
	}
	if budget.Used() != 0 {
		t.Fatalf("queue close leaked %d bytes", budget.Used())
	}
}

func TestEventQueueCancellationDoesNotReturnQueuedEvent(t *testing.T) {
	queue := NewEventQueue(NewMemoryBudget(0))
	arena := NewPayloadArena(nil)
	if err := queue.Push(Event{Header: testLease(t, arena, "queued")}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := queue.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Next = %v", err)
	}
	if _, err := queue.Next(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost queue terminal: %v", err)
	}
}

func TestEventQueueTransfersOwnershipBeforeSourceRelease(t *testing.T) {
	source, destination := NewMemoryBudget(0), NewMemoryBudget(0)
	queue := NewEventQueue(source)
	lease := testLease(t, NewPayloadArena(source), "owned")
	original := lease.state.release
	lease.state.release = func() {
		if destination.Used() == 0 {
			t.Error("source released before destination reserved")
		}
		original()
	}
	if err := queue.Push(Event{Delta: &PathDelta{Appended: []DeliveredHeader{{Header: lease}}}}); err != nil {
		t.Fatal(err)
	}
	e, release, err := queue.NextWithBudget(context.Background(), destination)
	if err != nil || release == nil || e.Delta.Appended[0].HeaderValue != "owned" {
		t.Fatalf("transfer = %+v, %v", e, err)
	}
	if source.Used() != 0 || destination.Used() == 0 {
		t.Fatal("wrong transfer accounting")
	}
	release()
	release()
	if destination.Used() != 0 {
		t.Fatal("destination leaked")
	}
}

func TestEventQueueFailedTransferPreservesSourceOwnership(t *testing.T) {
	source := NewMemoryBudget(0)
	queue := NewEventQueue(source)
	if err := queue.Push(Event{Header: testLease(t, NewPayloadArena(source), "owned")}); err != nil {
		t.Fatal(err)
	}
	before := source.Used()
	if _, release, err := queue.NextWithBudget(context.Background(), NewMemoryBudget(1)); !errors.Is(err, ErrMemoryBudget) || release == nil {
		t.Fatalf("failure = %v", err)
	}
	if source.Used() != before {
		t.Fatal("failed transfer changed source ownership")
	}
	queue.Abort()
	if source.Used() != 0 {
		t.Fatal("abort leaked failed transfer")
	}
}

func TestEventQueueTransfersProducerTerminalAudit(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			source, destination := NewMemoryBudget(0), NewMemoryBudget(0)
			queue := NewEventQueue(source)
			release, err := source.Reserve(100)
			if err != nil {
				t.Fatal(err)
			}
			var releases atomic.Int32
			a := &auditEntry{record: AuditRecord{Sequence: 1, Kind: InvalidObject}, release: func() {
				releases.Add(1)
				release()
			}}
			terminal := &TerminalError{Cause: ErrRecoveryExhausted, Audit: AuditRange{First: 1, Last: 1, first: a}}
			var reason error = terminal
			if wrapped {
				reason = fmt.Errorf("wrapped: %w", terminal)
			}
			_ = queue.Close(reason)
			_, retained, err := queue.NextWithBudget(context.Background(), destination)
			if !errors.Is(err, ErrRecoveryExhausted) || retained == nil {
				t.Fatalf("terminal: %v", err)
			}
			var got *TerminalError
			if !errors.As(err, &got) {
				t.Fatalf("terminal type lost: %v", err)
			}
			count := 0
			for record := range got.Audit.All() {
				if record.Sequence != 1 {
					t.Fatal(record)
				}
				count++
			}
			if count != 1 || source.Used() != 0 || destination.Used() == 0 {
				t.Fatal("terminal audit ownership lost")
			}
			retained()
			if destination.Used() != 0 {
				t.Fatal("terminal destination leaked")
			}
			if releases.Load() != 1 {
				t.Fatalf("terminal audit released %d times", releases.Load())
			}
		})
	}
}

func TestEventQueueReleasesTerminalOnAbortAndRepeatedClose(t *testing.T) {
	for _, test := range []struct {
		name  string
		clean func(*EventQueue, error)
	}{
		{name: "abort", clean: func(queue *EventQueue, reason error) {
			_ = queue.Close(reason)
			queue.Abort()
		}},
		{name: "repeated_close", clean: func(queue *EventQueue, reason error) {
			_ = queue.Close()
			_ = queue.Close(reason)
		}},
	} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", test.name, wrapped), func(t *testing.T) {
				source := NewMemoryBudget(0)
				queue := NewEventQueue(source)
				release, err := source.Reserve(100)
				if err != nil {
					t.Fatal(err)
				}
				var releases atomic.Int32
				entry := &auditEntry{record: AuditRecord{Sequence: 1, Kind: InvalidObject}, release: func() {
					releases.Add(1)
					release()
				}}
				terminal := &TerminalError{Cause: ErrRecoveryExhausted, Audit: AuditRange{First: 1, Last: 1, first: entry}}
				var reason error = terminal
				if wrapped {
					reason = fmt.Errorf("wrapped: %w", terminal)
				}
				test.clean(queue, reason)
				if source.Used() != 0 {
					t.Fatalf("terminal source budget leaked: %d", source.Used())
				}
				if releases.Load() != 1 {
					t.Fatalf("terminal audit released %d times", releases.Load())
				}
			})
		}
	}
}

func TestEventQueueFailedTransferKeepsAuditUnderSourceBudget(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			source := NewMemoryBudget(0)
			queue := NewEventQueue(source)
			release, err := source.Reserve(100)
			if err != nil {
				t.Fatal(err)
			}
			entry := &auditEntry{record: AuditRecord{Sequence: 7, Kind: InvalidObject}, release: release}
			audit := AuditRange{First: 7, Last: 7, first: entry}
			if terminal {
				_ = queue.Close(&TerminalError{Cause: ErrRecoveryExhausted, Audit: audit})
			} else {
				if err := queue.Push(Event{Header: testLease(t, NewPayloadArena(source), "payload"), Audit: audit}); err != nil {
					t.Fatal(err)
				}
			}
			_, hold, err := queue.NextWithBudget(context.Background(), NewMemoryBudget(1))
			var transfer *TransferError
			if !errors.Is(err, ErrMemoryBudget) || !errors.As(err, &transfer) || hold == nil {
				t.Fatalf("transfer failure=%v", err)
			}
			if terminal && !errors.Is(err, ErrRecoveryExhausted) {
				t.Fatalf("transfer lost producer cause: %v", err)
			}
			queue.Abort()
			if source.Used() != 100 {
				t.Fatalf("source released transferred audit: %d", source.Used())
			}
			count := 0
			for record := range transfer.Audit.All() {
				if record.Sequence != 7 {
					t.Fatal(record)
				}
				count++
			}
			if count != 1 {
				t.Fatal("audit lost")
			}
			hold()
			hold()
			if source.Used() != 0 {
				t.Fatal("audit leaked")
			}
		})
	}
}

func TestBudgetRejectsAtomicallyAndReleaseIsIdempotent(t *testing.T) {
	budget := NewMemoryBudget(10)
	first, err := budget.Reserve(6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(5); !errors.Is(err, ErrMemoryBudget) {
		t.Fatalf("over-budget reserve = %v", err)
	}
	if budget.Used() != 6 {
		t.Fatalf("failed reserve mutated usage: %d", budget.Used())
	}
	first()
	first()
	if budget.Used() != 0 {
		t.Fatalf("double release corrupted usage: %d", budget.Used())
	}
	if _, err := budget.Reserve(10); err != nil {
		t.Fatalf("reserve after release = %v", err)
	}
}

func TestPayloadLeaseTakeTransfersValueOnlyOnce(t *testing.T) {
	arena := NewPayloadArena(NewMemoryBudget(10))
	lease := testLease(t, arena, "payload")
	if lease.Bytes != 7 || lease.ID == 0 {
		t.Fatalf("lease = %+v", lease)
	}
	var released atomic.Int32
	original := lease.state.release
	lease.state.release = func() {
		released.Add(1)
		original()
	}
	if got := lease.take(); got != "payload" {
		t.Fatalf("take = %v", got)
	}
	if got := lease.take(); got != nil || released.Load() != 1 {
		t.Fatalf("second take = %v releases=%d", got, released.Load())
	}
}

func TestQueuePushDoesNotWaitForBlockedConsumer(t *testing.T) {
	queue := NewEventQueue(NewMemoryBudget(0))
	arena := NewPayloadArena(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = queue.Next(ctx) }()
	time.Sleep(time.Millisecond)
	for range 100 {
		if err := queue.Push(Event{Header: testLease(t, arena, "payload")}); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	<-done
	if budget := queue.budget; budget != nil {
		queue.Close()
	}
}

func TestAtomicDeltaPublicationDoesNotCommitOnBudgetFailure(t *testing.T) {
	budget := NewMemoryBudget(1)
	queue := NewEventQueue(budget)
	committed := false
	err := queue.Publish(Event{Delta: &PathDelta{Appended: []DeliveredHeader{{Node: chainNode(10, 1, AnchorNode{})}}}}, func(*Event) error { committed = true; return nil })
	if !errors.Is(err, ErrMemoryBudget) || committed || budget.Used() != 0 {
		t.Fatalf("partial commit=%v err=%v bytes=%d", committed, err, budget.Used())
	}
}
func BenchmarkEventQueuePushPop(b *testing.B) {
	for _, depth := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprint(depth), func(b *testing.B) {
			q := NewEventQueue(nil)
			for i := 0; i < depth; i++ {
				_ = q.Push(Event{})
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = q.Push(Event{})
				_, _ = q.Next(context.Background())
			}
			b.StopTimer()
			q.Abort()
		})
	}
}
