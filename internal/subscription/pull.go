package subscription

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PullRequest describes one same-source compensation probe for an event that
// was observed through Push but has not reached quorum. The request keeps the
// source identity and physical session generation alongside the callback so a
// late or cross-source answer cannot contribute a vote.
type PullRequest struct {
	Kind       Kind
	ContextKey string
	SourceID   string
	Generation uint64
	FirstSeen  time.Time
	Pull       func(context.Context) (Observation, error)
}

// PullProbeConfig bounds the Push-to-Pull window. Pulling never changes the
// snapshot denominator or quorum; it only supplies a missing vote from the
// same source.
type PullProbeConfig struct {
	SoftTimeout time.Duration
	HardTimeout time.Duration
}

// ProbeError preserves the source and phase when a Pull fails. The underlying
// error remains available through errors.Is/errors.As.
type ProbeError struct {
	SourceID   string
	Generation uint64
	ContextKey string
	Err        error
}

func (e *ProbeError) Error() string {
	return fmt.Sprintf("subscription: pull source=%q generation=%d context=%q: %v", e.SourceID, e.Generation, e.ContextKey, e.Err)
}

func (e *ProbeError) Unwrap() error { return e.Err }

// PullProbe owns one compensation window. It is intentionally separate from
// the WSS worker: owner cancellation stops every in-flight Pull, while the
// Aggregator remains the sole quorum and delivery state machine.
type PullProbe struct {
	cancel context.CancelFunc
	done   chan struct{}
	errs   chan error
	once   sync.Once
}

// StartPullProbe schedules same-source Pull requests after the soft deadline
// and advances the supplied Aggregator at the hard deadline. A Pull result is
// admitted only when source, generation, kind, and context identity all
// match the request that initiated it.
func StartPullProbe(ctx context.Context, agg *Aggregator, requests []PullRequest, cfg PullProbeConfig) (*PullProbe, error) {
	if agg == nil {
		return nil, errors.New("subscription: nil aggregator")
	}
	if len(requests) == 0 {
		return nil, errors.New("subscription: no pull requests")
	}
	if cfg.HardTimeout <= 0 || cfg.SoftTimeout < 0 || cfg.SoftTimeout >= cfg.HardTimeout {
		return nil, errors.New("subscription: invalid pull timeout window")
	}
	seen := make(map[string]struct{}, len(requests))
	firstSeen := time.Time{}
	for _, request := range requests {
		if request.SourceID == "" || request.Generation == 0 || request.ContextKey == "" || request.Pull == nil {
			return nil, errors.New("subscription: incomplete pull request")
		}
		if request.Kind != KindNewHeads && request.Kind != KindLogs && request.Kind != KindTransactionReceipts {
			return nil, fmt.Errorf("subscription: unsupported pull kind %q", request.Kind)
		}
		if _, exists := seen[request.SourceID]; exists {
			return nil, fmt.Errorf("subscription: duplicate pull source %q", request.SourceID)
		}
		seen[request.SourceID] = struct{}{}
		if request.FirstSeen.IsZero() {
			request.FirstSeen = time.Now()
		}
		if firstSeen.IsZero() || request.FirstSeen.Before(firstSeen) {
			firstSeen = request.FirstSeen
		}
	}
	probeCtx, cancel := context.WithCancel(ctx)
	p := &PullProbe{cancel: cancel, done: make(chan struct{}), errs: make(chan error, len(requests)+1)}
	go p.run(probeCtx, cancel, agg, requests, cfg, firstSeen)
	return p, nil
}

func (p *PullProbe) Err() <-chan error { return p.errs }

// Stop cancels all in-flight Pulls and is idempotent.
func (p *PullProbe) Stop() {
	p.once.Do(func() { p.cancel() })
	<-p.done
}

func (p *PullProbe) run(ctx context.Context, cancel context.CancelFunc, agg *Aggregator, requests []PullRequest, cfg PullProbeConfig, firstSeen time.Time) {
	defer close(p.done)
	defer close(p.errs)
	soft := time.NewTimer(time.Until(firstSeen.Add(cfg.SoftTimeout)))
	if !soft.Stop() {
		<-soft.C
	}
	soft.Reset(time.Until(firstSeen.Add(cfg.SoftTimeout)))
	hard := time.NewTimer(time.Until(firstSeen.Add(cfg.HardTimeout)))
	if !hard.Stop() {
		<-hard.C
	}
	hard.Reset(time.Until(firstSeen.Add(cfg.HardTimeout)))
	defer soft.Stop()
	defer hard.Stop()

	var pulls sync.WaitGroup
	startPulls := func() {
		for _, request := range requests {
			request := request
			pulls.Add(1)
			go func() {
				defer pulls.Done()
				observation, err := PullObservation(ctx, request.SourceID, request.Generation, request.Pull)
				if err != nil {
					p.errs <- &ProbeError{SourceID: request.SourceID, Generation: request.Generation, ContextKey: request.ContextKey, Err: err}
					return
				}
				if observation.Kind != request.Kind || observation.ContextKey != request.ContextKey {
					p.errs <- &ProbeError{SourceID: request.SourceID, Generation: request.Generation, ContextKey: request.ContextKey, Err: fmt.Errorf("%w: pull event identity mismatch", ErrUnknownSource)}
					return
				}
				if err := agg.Submit(observation); err != nil && !errors.Is(err, ErrStaleGeneration) && !errors.Is(err, ErrUnknownSource) {
					p.errs <- &ProbeError{SourceID: request.SourceID, Generation: request.Generation, ContextKey: request.ContextKey, Err: err}
				}
			}()
		}
	}

	select {
	case <-ctx.Done():
		cancel()
		return
	case <-soft.C:
		startPulls()
	}
	select {
	case <-ctx.Done():
		cancel()
	case <-hard.C:
		agg.Advance(firstSeen.Add(cfg.HardTimeout))
		cancel()
	}
	pulls.Wait()
}
