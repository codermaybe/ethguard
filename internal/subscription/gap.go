package subscription

import (
	"errors"
	"sync"
)

var ErrHeadGap = errors.New("subscription: new-head gap")

type HeadGap struct {
	From uint64
	To   uint64
}

// HeadGapTracker observes canonical head heights per source. It reports a
// skipped range without deciding whether the cause was a reorg; callers may
// route the gap to Pull or Err according to subscription policy.
type HeadGapTracker struct {
	mu   sync.Mutex
	last map[string]uint64
}

func NewHeadGapTracker() *HeadGapTracker { return &HeadGapTracker{last: make(map[string]uint64)} }

func (t *HeadGapTracker) Observe(source string, height uint64) *HeadGap {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, ok := t.last[source]
	if !ok {
		t.last[source] = height
		return nil
	}
	// Reorgs and duplicate notifications can move a source backwards. They do
	// not prove a forward gap, and must not reset the high-water mark used by
	// the next canonical head.
	if height <= previous {
		return nil
	}
	t.last[source] = height
	if height == previous+1 {
		return nil
	}
	return &HeadGap{From: previous + 1, To: height - 1}
}
