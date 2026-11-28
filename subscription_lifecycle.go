package ethguard

import "fmt"

// SubscriptionLifecycle joins an external subscription delivery worker to this
// client's closing barrier. Stop must be idempotent and nonblocking. Done must
// be stable and close only after native Close, delivery exit, and Err closure.
type SubscriptionLifecycle interface {
	Stop()
	Done() <-chan struct{}
}

type attachedSubscription struct {
	life SubscriptionLifecycle
	done <-chan struct{}
}

// AttachSubscription registers a delivery worker before its native setup starts.
// The returned detach only removes a completed worker: calling it before Done
// closes has no effect and it may be called again after completion. Registration
// after closing starts fails with ErrPoolClosed.
func (c *Client) AttachSubscription(life SubscriptionLifecycle) (func(), error) {
	if c == nil || life == nil {
		return nil, fmt.Errorf("%w: missing subscription lifecycle", ErrInvalidRequest)
	}
	done := life.Done()
	if done == nil {
		return nil, fmt.Errorf("%w: subscription lifecycle has no Done channel", ErrInvalidRequest)
	}
	entry := &attachedSubscription{life: life, done: done}
	c.subscriptionMu.Lock()
	defer c.subscriptionMu.Unlock()
	if c.closed.Load() {
		return nil, ErrPoolClosed
	}
	if c.subscriptions == nil {
		c.subscriptions = make(map[*attachedSubscription]struct{})
	}
	c.subscriptions[entry] = struct{}{}
	return func() {
		select {
		case <-done:
		default:
			return
		}
		c.subscriptionMu.Lock()
		delete(c.subscriptions, entry)
		c.subscriptionMu.Unlock()
	}, nil
}
