package subscription

import (
	"errors"
	"fmt"
)

var (
	ErrPayloadTooLarge = errors.New("subscription: event payload exceeds limit")
	ErrEvidenceBudget  = errors.New("subscription: evidence budget exhausted")
)

// Budget is an optional guard for raw material retained by a subscription.
// Zero values preserve the legacy in-memory aggregator behavior; the public
// bridge supplies non-zero values when it enables source evidence collection.
type Budget struct {
	MaxPayloadBytes  int
	MaxEvidenceBytes int64
}

func (b Budget) validatePayload(payload []byte) error {
	if b.MaxPayloadBytes > 0 && len(payload) > b.MaxPayloadBytes {
		return fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), b.MaxPayloadBytes)
	}
	return nil
}
