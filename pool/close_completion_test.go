package pool

import (
	"context"
	"encoding/json"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
)

// blockingCloseTransport records when Close is entered and when it actually
// completes; Close blocks until the test releases it.
type blockingCloseTransport struct {
	mu        sync.Mutex
	closeDone bool
	entered   chan struct{}
	release   chan struct{}
}

func (t *blockingCloseTransport) CallContext(_ context.Context, result any, _ string, _ ...any) error {
	*result.(*json.RawMessage) = json.RawMessage(`"0x1"`)
	return nil
}

func (t *blockingCloseTransport) Close() {
	t.mu.Lock()
	closed := t.closeDone
	t.mu.Unlock()
	if closed {
		return
	}
	close(t.entered)
	<-t.release
	t.mu.Lock()
	t.closeDone = true
	t.mu.Unlock()
}

func (t *blockingCloseTransport) ChainID(context.Context) (*big.Int, error) {
	return big.NewInt(1), nil
}

func (t *blockingCloseTransport) closeCompleted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeDone
}

type oneShotDialer struct{ tr *blockingCloseTransport }

func (d oneShotDialer) DialContext(context.Context, string) (transport.Transport, error) {
	return d.tr, nil
}

// TestConcurrentCloseWaitsForResourceClosure is the shared-completion
// contract: with one transport whose Close blocks, the first Close runs the
// close sequence and every concurrent caller — second, third — returns only
// after the transport has actually closed, never while the sequence is
// still in flight.
func TestConcurrentCloseWaitsForResourceClosure(t *testing.T) {
	tr := &blockingCloseTransport{entered: make(chan struct{}), release: make(chan struct{})}
	p, err := NewWithMembers(1, []Member{{
		Name:   "provider",
		URL:    "https://provider.example",
		Dialer: oneShotDialer{tr},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Warm the source so its transport exists and is closeable.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatalf("warm call: %v", err)
	}

	const closers = 4
	var wg sync.WaitGroup
	returned := make(chan struct{}, closers)
	for i := 0; i < closers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Close()
			returned <- struct{}{}
		}()
	}
	// The close sequence is in flight once the transport's Close is entered.
	select {
	case <-tr.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the close sequence never reached the transport")
	}

	for i := 0; i < closers-1; i++ {
		select {
		case <-returned:
			t.Fatal("a Close returned while the transport was still closing")
		case <-time.After(50 * time.Millisecond):
		}
	}
	close(tr.release) // let the transport's Close finish
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Closers did not all return after completion")
	}
	for i := 0; i < closers; i++ {
		select {
		case <-returned:
		default:
			t.Fatal("a Close caller never returned")
		}
	}
	if !tr.closeCompleted() {
		t.Fatal("Close callers returned before the transport finished closing")
	}
	// A further Close returns under the same completed conditions.
	p.Close()
}
