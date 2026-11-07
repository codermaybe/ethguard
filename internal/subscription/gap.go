package subscription

import "errors"

var ErrHeadGap = errors.New("subscription: new-head gap")

type HeadGap struct {
	From uint64
	To   uint64
}

// HeadGapTracker observes canonical head heights per source. It reports a
// skipped range without deciding whether the cause was a reorg; callers may
// route the gap to Pull or Err according to subscription policy.
type HeadGapTracker struct {
	last map[string]uint64
}

func NewHeadGapTracker() *HeadGapTracker { return &HeadGapTracker{last: make(map[string]uint64)} }

func (t *HeadGapTracker) Observe(source string, height uint64) *HeadGap {
	previous, ok := t.last[source]
	t.last[source] = height
	if !ok || height <= previous+1 {
		return nil
	}
	return &HeadGap{From: previous + 1, To: height - 1}
}
