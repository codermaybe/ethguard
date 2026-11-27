package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// Source supplies one fixed member's subscription capability. Content and
// recovery requests use its unary or full-response capability when present;
// the subscription owner never creates or owns the underlying connection.
type Source interface {
	ID() string
	Subscribe(context.Context, string, ...any) (transport.Subscription, error)
}

type subscriptionUnarySource interface {
	CallContext(context.Context, string, ...any) (json.RawMessage, error)
}

// snapshotSources admits the fixed WSS membership at the entry's membership
// stage. Source health cannot change this denominator.
func snapshotSources(load func() ([]Source, error), mode rpc.CorroborationMode) ([]Source, error) {
	sources, err := load()
	if err != nil {
		return nil, err
	}
	if len(sources) == 1 && mode != rpc.CorroborationForwarded {
		return nil, fmt.Errorf("%w: one WSS source cannot satisfy strict subscription corroboration; configure at least two WSS sources", rpc.ErrInvalidRequest)
	}
	return sources, nil
}

// NewHead captures fixed membership before validating and starting its stream.
func NewHead(owner, setup context.Context, load func() ([]Source, error), p HeadSubscriptionPolicy, mode rpc.CorroborationMode) (*HeadSubscription, error) {
	sources, err := snapshotSources(load, mode)
	if err != nil {
		return nil, err
	}
	return startNewHeadSubscription(owner, setup, sources, p, mode)
}

func waitSubscriptionRetry(stop <-chan struct{}, ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
