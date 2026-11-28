package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/rpc"
)

func TestIndependentHeadIntentionalTerminalPrecedesVisibleQueueAbort(t *testing.T) {
	for _, cause := range []string{"close", "owner_cancel"} {
		t.Run(cause, func(t *testing.T) {
			owner, cancel := context.WithCancel(context.Background())
			defer cancel()
			source, _ := newHeadSource("independent")
			s, err := startNewHeadSubscription(owner, context.Background(), []Source{source}, DefaultHeadSubscriptionPolicy(), rpc.CorroborationForwarded)
			if err != nil {
				t.Fatal(err)
			}
			s.queue.mu.Lock()
			var closeDone chan struct{}
			if cause == "close" {
				closeDone = make(chan struct{})
				go func() { _ = s.Close(); close(closeDone) }()
			} else {
				cancel()
			}
			// Freeze queue shutdown. Nil producer termination must become
			// observable before a consumer can see ErrClosed from that queue.
			terminalReady := false
			select {
			case <-s.Terminated():
				terminalReady = true
			case <-time.After(5 * time.Second):
			}
			s.queue.mu.Unlock()
			if !terminalReady {
				t.Error("intentional producer terminal waited behind queue abort")
			}
			if closeDone != nil {
				<-closeDone
			} else if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.Terminated():
			default:
				t.Fatal("completed intentional close has no terminal signal")
			}
			if err := s.TerminalError(); err != nil {
				t.Fatalf("intentional close acquired a failure: %v", err)
			}
			if s.budget.Used() != 0 {
				t.Fatal("close retained native ownership")
			}
		})
	}
}
