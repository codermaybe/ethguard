package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// FinalizedHead resolves finalized-block observations by selecting the lowest
// non-stale finalized height. Views trailing the freshest observation by more
// than maxLag blocks are rejected; conflicting hashes at the selected height
// fail closed.
func FinalizedHead(maxLag uint64) Policy {
	return finalizedHeadPolicy{maxLag: maxLag}
}

type finalizedHeadPolicy struct {
	maxLag uint64
}

type finalizedHead struct {
	Number string `json:"number"`
	Hash   string `json:"hash"`
}

func (p finalizedHeadPolicy) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

// Normalize reduces a finalized block object to "<height>|<hash>" with the
// height in decimal, so equivalent hexadecimal spellings cluster together.
func (p finalizedHeadPolicy) Normalize(value json.RawMessage) (string, error) {
	head, err := decodeFinalizedHead(value)
	if err != nil {
		return "", err
	}
	height, err := parseFinalizedHeight(head.Number)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d|%s", height, strings.ToLower(head.Hash)), nil
}

func (p finalizedHeadPolicy) Resolve(obs []Observation) (Decision, error) {
	var decision Decision
	type view struct {
		source SourceRef
		height uint64
		hash   string
		value  json.RawMessage
	}
	views := make([]view, 0, len(obs))
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		head, err := decodeFinalizedHead(item.Value)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		height, err := parseFinalizedHeight(head.Number)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		views = append(views, view{source: item.Source, height: height, hash: strings.ToLower(head.Hash), value: item.Value})
	}
	if len(views) == 0 {
		return Decision{Faults: decision.Faults}, fmt.Errorf(
			"finalized head: no source produced a valid response (%d attempted): %w",
			len(obs), ErrNoValidResponse)
	}

	var freshest uint64
	for _, v := range views {
		if v.height > freshest {
			freshest = v.height
		}
	}

	clusters := make(map[uint64][]SourceRef)
	hashes := make(map[uint64]string)
	values := make(map[uint64]json.RawMessage)
	validViews := make([]view, 0, len(views))
	for _, v := range views {
		if freshest-v.height > p.maxLag {
			decision.Reject = append(decision.Reject, v.source)
			decision.Faults = append(decision.Faults, fmt.Errorf(
				"source %q: stale finalized height %d trails %d beyond %d blocks",
				v.source.Name, v.height, freshest, p.maxLag))
			continue
		}
		validViews = append(validViews, v)
		clusters[v.height] = append(clusters[v.height], v.source)
		if _, seen := hashes[v.height]; !seen {
			hashes[v.height] = v.hash
			values[v.height] = v.value
		}
	}
	if len(clusters) == 0 {
		return decision, fmt.Errorf("finalized head: all source views are stale: %w", ErrNoValidResponse)
	}

	heights := make([]uint64, 0, len(clusters))
	for h := range clusters {
		heights = append(heights, h)
	}
	slices.Sort(heights)
	winner := heights[0]

	distinct := make(map[string]bool, len(clusters[winner]))
	for _, v := range validViews {
		if v.height == winner {
			distinct[v.hash] = true
		}
	}
	if len(distinct) > 1 {
		decision.Value = values[winner]
		decision.Support = clusters[winner]
		return decision, fmt.Errorf("finalized head: %d hashes claimed for height %d: %w",
			len(distinct), winner, ErrConflict)
	}

	decision.Status = Verified
	if len(clusters[winner]) == 1 {
		decision.Status = Selected
	}
	decision.Value = values[winner]
	decision.Support = clusters[winner]
	return decision, nil
}

func decodeFinalizedHead(value json.RawMessage) (finalizedHead, error) {
	var head finalizedHead
	if err := json.Unmarshal(value, &head); err != nil {
		return head, err
	}
	if head.Number == "" || len(head.Hash) < 3 {
		return head, fmt.Errorf("finalized head: missing number or hash")
	}
	return head, nil
}

func parseFinalizedHeight(number string) (uint64, error) {
	digits := strings.TrimPrefix(number, "0x")
	height, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("finalized head: invalid height %q", number)
	}
	return height, nil
}
