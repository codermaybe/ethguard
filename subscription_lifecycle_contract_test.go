package ethguard_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpctest"
)

type independentLifecycle struct {
	stopped  chan struct{}
	done     chan struct{}
	onStop   func()
	stopOnce sync.Once
}

func (l *independentLifecycle) Stop() {
	l.stopOnce.Do(func() {
		if l.onStop != nil {
			l.onStop()
		}
		close(l.stopped)
	})
}
func (l *independentLifecycle) Done() <-chan struct{} { return l.done }

func independentLifecycleClient(t *testing.T) *ethguard.Client {
	t.Helper()
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	client, err := ethguard.Dial(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func independentClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not complete", what)
	}
}

func TestIndependentSubscriptionLifecycleEarlyDetachCannotEscapeCloseBarrier(t *testing.T) {
	client := independentLifecycleClient(t)
	life := &independentLifecycle{stopped: make(chan struct{}), done: make(chan struct{})}
	finish := sync.OnceFunc(func() { close(life.done) })
	t.Cleanup(finish)
	detach, err := client.AttachSubscription(life)
	if err != nil {
		t.Fatal(err)
	}
	detach()
	closed := make(chan struct{})
	go func() { client.Close(); close(closed) }()
	independentClosed(t, life.stopped, "registered lifecycle stop")
	select {
	case <-closed:
		t.Fatal("Close returned before native cleanup/delivery completion")
	default:
	}
	finish()
	independentClosed(t, closed, "Close barrier")
	detach()
	detach()
	if _, err := client.AttachSubscription(&independentLifecycle{stopped: make(chan struct{}), done: make(chan struct{})}); !errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatalf("closed owner admitted new work: %v", err)
	}
}

func TestIndependentSubscriptionLifecycleStopRunsOutsideRegistryLock(t *testing.T) {
	client := independentLifecycleClient(t)
	life := &independentLifecycle{stopped: make(chan struct{}), done: make(chan struct{})}
	callback := make(chan error, 1)
	life.onStop = func() {
		other := &independentLifecycle{stopped: make(chan struct{}), done: make(chan struct{})}
		_, err := client.AttachSubscription(other)
		callback <- err
		close(life.done)
	}
	if _, err := client.AttachSubscription(life); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { client.Close(); close(closed) }()
	select {
	case err := <-callback:
		if !errors.Is(err, ethguard.ErrPoolClosed) {
			t.Fatalf("stop callback observed open registry: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop callback blocked on registry lock")
	}
	independentClosed(t, closed, "Close with reentrant registry probe")
}

func TestIndependentSubscriptionLifecycleConcurrentAttachAndCloseShareBarrier(t *testing.T) {
	for round := 0; round < 20; round++ {
		client := independentLifecycleClient(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var accepted atomic.Int32
		var stopped atomic.Int32
		for range 16 {
			wg.Go(func() {
				<-start
				life := &independentLifecycle{stopped: make(chan struct{}), done: make(chan struct{})}
				life.onStop = func() { stopped.Add(1); close(life.done) }
				detach, err := client.AttachSubscription(life)
				if err != nil {
					if !errors.Is(err, ethguard.ErrPoolClosed) {
						t.Errorf("attach race: %v", err)
					}
					return
				}
				accepted.Add(1)
				detach() // An early caller detach cannot remove unfinished work.
				<-life.done
				detach()
			})
		}
		for range 4 {
			wg.Go(func() { <-start; client.Close() })
		}
		close(start)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		independentClosed(t, done, "concurrent attachment and repeated close")
		if accepted.Load() != stopped.Load() {
			t.Fatalf("accepted=%d stopped=%d", accepted.Load(), stopped.Load())
		}
	}
}
