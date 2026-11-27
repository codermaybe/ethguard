package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/core/types"
)

// Notification leases belong to the Head reader after receipt, including
// inputs rejected before decoding or while decoding.
func TestHeadReleasesAcceptedAndRejectedNotifications(t *testing.T) {
	for _, shape := range []string{"valid", "invalid", "foreign-source", "foreign-generation"} {
		t.Run(shape, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				source, feed := newHeadSource("a")
				s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{source}, testHeadPolicy(), rpc.CorroborationForwarded)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
				var releases atomic.Int32
				event := transport.Notification{
					Result:  testHeader(&types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}),
					Release: func() { releases.Add(1) },
				}
				switch shape {
				case "invalid":
					event.Result = json.RawMessage(`{"hash":"bad"}`)
				case "foreign-source":
					event.SourceID = "other"
				case "foreign-generation":
					event.Generation = 99
				}
				feed.events <- event
				synctest.Wait()
				if releases.Load() != 1 {
					t.Fatalf("released %d times", releases.Load())
				}
				if shape == "valid" {
					if _, err := s.Next(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				_ = s.Close()
				if s.budget.Used() != 0 {
					t.Fatal("notification cleanup retained budget")
				}
			})
		})
	}
}

type headEstablishSource struct {
	*newHeadTestSource
	establish func() (transport.Subscription, error)
}

type countedHeadFeed struct {
	*newHeadTestFeed
	reads, unsubscribes atomic.Int32
}

func (f *countedHeadFeed) Events() <-chan transport.Notification {
	f.reads.Add(1)
	return f.newHeadTestFeed.Events()
}
func (f *countedHeadFeed) Unsubscribe() {
	f.unsubscribes.Add(1)
	f.newHeadTestFeed.Unsubscribe()
}

func (s *headEstablishSource) SubscribeBounded(context.Context, transport.NotificationPolicy, string, ...any) (transport.Subscription, error) {
	return s.establish()
}

// A successful registration may land after owner cancellation. The registered
// transport still has an owner, and Close must join that work and its cleanup.
func TestHeadEstablishmentAndReconnectJoinOwnerClose(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		t.Run(fmt.Sprint(reconnect), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				source, first := newHeadSource("a")
				_, lateFeed := newHeadSource("late")
				late := &countedHeadFeed{newHeadTestFeed: lateFeed}
				firstOwned := &countedHeadFeed{newHeadTestFeed: first}
				started, release, disposed := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
				cleaning, allowCleanup := make(chan struct{}, 2), make(chan struct{})
				releaseOnce := sync.OnceFunc(func() { close(release) })
				cleanupOnce := sync.OnceFunc(func() { close(allowCleanup) })
				var eventReleases atomic.Int32
				late.events <- transport.Notification{Result: testHeader(&types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}), Release: func() { eventReleases.Add(1) }}
				late.cleanup = func() {
					cleaning <- struct{}{}
					<-allowCleanup
					select {
					case event := <-late.events:
						if event.Release != nil {
							event.Release()
						}
					default:
					}
					disposed <- struct{}{}
				}
				var calls int
				blocking := &headEstablishSource{source, func() (transport.Subscription, error) {
					calls++
					if reconnect && calls == 1 {
						return firstOwned, nil
					}
					close(started)
					<-release
					return late, nil
				}}
				owner, cancel := context.WithCancel(context.Background())
				p := testHeadPolicy()
				p.RetryDelay = time.Millisecond
				s, err := startNewHeadSubscription(owner, context.Background(), []Source{blocking}, p, rpc.CorroborationForwarded)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { cancel(); releaseOnce(); cleanupOnce(); _ = s.Close() }()
				if reconnect {
					close(first.events)
					close(first.errs)
				}
				<-started
				cancel()
				closed := make(chan struct{})
				go func() { _ = s.Close(); close(closed) }()
				synctest.Wait()
				select {
				case <-closed:
					t.Fatal("Close escaped in-flight registration")
				default:
				}
				releaseOnce()
				synctest.Wait()
				select {
				case <-cleaning:
				default:
					t.Fatal("late registration did not start cleanup")
				}
				select {
				case <-closed:
					t.Fatal("Close escaped late inner cleanup")
				default:
				}
				if late.reads.Load() != 0 {
					t.Errorf("stopped subscription read late inner %d times", late.reads.Load())
				}
				cleanupOnce()
				synctest.Wait()
				select {
				case <-disposed:
				default:
					t.Fatal("late successful registration leaked")
				}
				select {
				case <-closed:
				default:
					t.Fatal("Close did not join registration cleanup")
				}
				if s.budget.Used() != 0 {
					t.Fatal("registration cleanup retained budget")
				}
				if late.unsubscribes.Load() != 1 || eventReleases.Load() != 1 || (reconnect && firstOwned.unsubscribes.Load() != 1) {
					t.Fatalf("ownership: late unsubscribes=%d first unsubscribes=%d released=%d", late.unsubscribes.Load(), firstOwned.unsubscribes.Load(), eventReleases.Load())
				}
			})
		})
	}
}

func TestHeadExhaustedSourcesPublishTerminalBeforeSlowCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source, feed := newHeadSource("a")
		cleaning, release := make(chan struct{}, 1), make(chan struct{})
		releaseOnce := sync.OnceFunc(func() { close(release) })
		var unsubscribes atomic.Int32
		feed.cleanup = func() { unsubscribes.Add(1); cleaning <- struct{}{}; <-release }
		s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{source}, testHeadPolicy(), rpc.CorroborationForwarded)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { releaseOnce(); _ = s.Close() }()
		first := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
		second := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: first.Hash()}
		feed.events <- transport.Notification{Result: testHeader(first)}
		synctest.Wait()
		feed.events <- transport.Notification{Result: testHeader(second)}
		synctest.Wait()
		close(feed.events)
		close(feed.errs)
		<-cleaning
		for version := uint64(1); version <= 2; version++ {
			event, err := s.Next(context.Background())
			if err != nil || event.Path.NewVersion != version {
				t.Fatalf("FIFO: %+v %v", event, err)
			}
		}
		terminal := make(chan error, 1)
		go func() { _, err := s.Next(context.Background()); terminal <- err }()
		synctest.Wait()
		select {
		case err := <-terminal:
			var ended *HeadTerminalError
			if !errors.As(err, &ended) || ended.Audit.First == 0 {
				t.Errorf("terminal: %v", err)
			}
			if _, err := s.Next(context.Background()); !errors.Is(err, ErrNextAfterTerminal) {
				t.Errorf("terminal repeated: %v", err)
			}
		default:
			t.Error("source exhaustion hid terminal behind slow Unsubscribe")
		}
		closed := make(chan struct{})
		go func() { _ = s.Close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
			t.Error("Close escaped real cleanup")
		default:
		}
		releaseOnce()
		<-closed
		if unsubscribes.Load() != 1 || s.budget.Used() != 0 {
			t.Fatalf("cleanup calls=%d budget=%d", unsubscribes.Load(), s.budget.Used())
		}
	})
}

type delayedHeadIdentitySource struct {
	*newHeadTestSource
	ids, subscriptions                atomic.Int32
	blockCall                         int32
	beforeAdmission, releaseAdmission chan struct{}
}

func (s *delayedHeadIdentitySource) ID() string {
	blockCall := s.blockCall
	if blockCall == 0 {
		blockCall = 2
	}
	if s.ids.Add(1) == blockCall {
		close(s.beforeAdmission)
		<-s.releaseAdmission
	}
	return s.id
}
func (s *delayedHeadIdentitySource) SubscribeBounded(context.Context, transport.NotificationPolicy, string, ...any) (transport.Subscription, error) {
	s.subscriptions.Add(1)
	return s.feed, nil
}

func TestHeadStopBeforeSessionAdmissionPreventsSubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source, _ := newHeadSource("a")
		delayed := &delayedHeadIdentitySource{newHeadTestSource: source, beforeAdmission: make(chan struct{}), releaseAdmission: make(chan struct{})}
		release := sync.OnceFunc(func() { close(delayed.releaseAdmission) })
		s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{delayed}, testHeadPolicy(), rpc.CorroborationForwarded)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { release(); _ = s.Close() }()
		<-delayed.beforeAdmission
		closed := make(chan struct{})
		go func() { _ = s.Close(); close(closed) }()
		synctest.Wait()
		release()
		<-closed
		if delayed.subscriptions.Load() != 0 {
			t.Fatal("stop lost session admission race")
		}
		if _, err := s.sessions.Current("a"); err == nil {
			t.Fatal("stopped source retained active session")
		}
		select {
		case <-s.watcherDone:
		default:
			t.Fatal("Close did not join owner watcher")
		}
		if s.budget.Used() != 0 {
			t.Fatal("unadmitted source retained budget")
		}
	})
}

func TestHeadBindFailureRetiresOpenedSessionBeforeOtherCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fa := newHeadSource("a")
		b, fb := newHeadSource("b")
		delayed := &delayedHeadIdentitySource{newHeadTestSource: b, blockCall: 3, beforeAdmission: make(chan struct{}), releaseAdmission: make(chan struct{})}
		releaseAdmission := sync.OnceFunc(func() { close(delayed.releaseAdmission) })
		cleaning, cleanup := make(chan struct{}, 1), make(chan struct{})
		releaseCleanup := sync.OnceFunc(func() { close(cleanup) })
		fa.cleanup = func() { cleaning <- struct{}{}; <-cleanup }
		p := testHeadPolicy()
		p.RetryDelay = time.Millisecond
		s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{a, delayed}, p, rpc.CorroborationMajority)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { releaseAdmission(); releaseCleanup(); _ = s.Close() }()
		synctest.Wait()
		close(fb.events)
		close(fb.errs)
		<-delayed.beforeAdmission
		synctest.Wait()
		hold, err := s.budget.Reserve(s.policy.MemoryBytes - s.budget.Used() - 1)
		if err != nil {
			t.Fatal(err)
		}
		defer hold()
		releaseAdmission()
		<-cleaning
		if _, err := s.sessions.Current("b"); !errors.Is(err, ErrSessionStale) {
			t.Fatalf("failed Bind retained opened generation: %v", err)
		}
		if delayed.subscriptions.Load() != 1 {
			t.Fatal("failed rebind started another Subscribe")
		}
		_, err = s.Next(context.Background())
		if !errors.Is(err, ErrMemoryBudget) {
			t.Fatalf("Bind failure: %v", err)
		}
		releaseCleanup()
		_ = s.Close()
		hold()
		if s.budget.Used() != 0 {
			t.Fatal("Bind failure leaked")
		}
	})
}

func TestHeadSetupFailureRetiresSessionAndKeepsRetryOpportunity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source, feed := newHeadSource("a")
		var attempts atomic.Int32
		retrying := &headEstablishSource{source, func() (transport.Subscription, error) {
			if attempts.Add(1) == 1 {
				return nil, errors.New("setup failed")
			}
			return feed, nil
		}}
		p := testHeadPolicy()
		p.RetryDelay = time.Hour
		s, err := startNewHeadSubscription(context.Background(), context.Background(), []Source{retrying}, p, rpc.CorroborationForwarded)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("missing failed setup")
		}
		if _, err := s.sessions.Current("a"); !errors.Is(err, ErrSessionStale) {
			t.Fatalf("failed setup remained active: %v", err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		session, err := s.sessions.Current("a")
		if err != nil || session.Generation != 2 {
			t.Fatalf("retry session: %+v %v", session, err)
		}
		feed.events <- transport.Notification{Result: testHeader(&types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)})}
		event, err := s.Next(context.Background())
		if err != nil || event.Path.NewVersion != 1 {
			t.Fatalf("retry prematurely exhausted: %+v %v", event, err)
		}
		found := false
		for audit := range event.Audit.All() {
			found = found || audit.Reason == HeadSourceSetupFailure
		}
		if !found {
			t.Fatal("setup failure lost audit")
		}
	})
}

func TestHeadConcurrentCloseJoinsEstimatorAndOwnerWatcher(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, fa := newHeadSource("a")
		b, fb := newHeadSource("b")
		started, release := make(chan struct{}, 2), make(chan struct{})
		releaseOnce := sync.OnceFunc(func() { close(release) })
		var s *HeadSubscription
		var callbacks atomic.Int32
		p := testHeadPolicy()
		p.Gate = HeadGatePolicy{Initial: time.Millisecond, Min: time.Millisecond, Max: time.Second, Estimate: func(HeadCommittedSample) time.Duration {
			callbacks.Add(1)
			_ = s.Basis()
			started <- struct{}{}
			<-release
			return time.Millisecond
		}}
		var err error
		s, err = startNewHeadSubscription(context.Background(), context.Background(), []Source{a, b}, p, rpc.CorroborationMajority)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { releaseOnce(); _ = s.Close() }()
		first := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)}
		pushLogsHead([]*newHeadTestFeed{fa, fb}, first)
		if _, err := s.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-started
		second := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), ParentHash: first.Hash()}
		pushLogsHead([]*newHeadTestFeed{fa, fb}, second)
		if _, err := s.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
		closed := make(chan struct{}, 3)
		for range 3 {
			go func() { _ = s.Close(); closed <- struct{}{} }()
		}
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close skipped admitted Estimate callback")
		default:
		}
		releaseOnce()
		for range 3 {
			<-closed
		}
		if callbacks.Load() != 1 {
			t.Fatal("Estimate started for unclaimed sample after stop")
		}
		select {
		case <-s.watcherDone:
		default:
			t.Fatal("Close skipped owner watcher")
		}
		if s.budget.Used() != 0 {
			t.Fatal("estimator retained budget")
		}
	})
}

func TestHeadVisibleOwnerCancellationWinsBeforeWatcherObtainsControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source, _ := newHeadSource("a")
		owner, cancel := context.WithCancel(context.Background())
		s, err := startNewHeadSubscription(owner, context.Background(), []Source{source}, testHeadPolicy(), rpc.CorroborationForwarded)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close() //nolint:errcheck // Subscription cleanup only joins work and releases memory; Close always returns nil.
		synctest.Wait()
		// Hold the same source-control seam so the owner watcher cannot request
		// stop first. A source failure reaching it must observe owner cancellation.
		s.mu.Lock()
		cancel()
		s.finishLocked(errors.New("source sessions ended"))
		s.mu.Unlock()
		if _, err := s.Next(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("owner cancellation became producer terminal: %v", err)
		}
		_ = s.Close()
		select {
		case <-s.watcherDone:
		default:
			t.Fatal("owner watcher not joined")
		}
	})
}
