package subscription

import (
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"strconv"
	"testing"
	"time"
	"unsafe"
)

func chainNode(number uint64, branch uint64, parent AnchorNode) AnchorNode {
	var hash [32]byte
	binary.BigEndian.PutUint64(hash[:8], number+1)
	binary.BigEndian.PutUint64(hash[8:16], branch)
	return AnchorNode{Number: number, Hash: hash, ParentHash: parent.Hash}
}

func newTracker(t testing.TB, retained int) (*HeadTracker, *EventQueue, *AtomicMemoryBudget) {
	t.Helper()
	b := NewMemoryBudget(16 << 20)
	tr, err := NewHeadTracker(HeadTrackerConfig{DomainID: "test", Members: []string{"a", "b", "c"}, Quorum: 2, Mode: StrictMode, RetainedNodes: retained, Budget: b})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := tr.Bind(SourceSession{id, 1}, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	q := NewEventQueue(b)
	t.Cleanup(func() {
		q.Abort()
		tr.Close()
		if b.Used() != 0 {
			t.Errorf("retained %d bytes after Close", b.Used())
		}
	})
	return tr, q, b
}

func TestHeadWorkspaceRejectsSyntheticAuditOverflow(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("synthetic audit count requires a 64-bit int")
	}
	// Only the count is synthetic; no audit records or large slices are built.
	audits := 1
	audits <<= 61
	budget := NewMemoryBudget(0)
	tracker := &HeadTracker{cfg: HeadTrackerConfig{Budget: budget}, auditCount: audits}
	if release, err := tracker.reserveWork(); !errors.Is(err, ErrMemoryBudget) || release != nil {
		if release != nil {
			release()
		}
		t.Fatalf("synthetic audit overflow = %v, want memory budget failure", err)
	}
	if budget.Used() != 0 {
		t.Fatalf("overflow reserved %d bytes", budget.Used())
	}
}

func TestHeadWorkspaceArithmeticBoundaries(t *testing.T) {
	const maximum = ^uint64(0)
	auditBytes := 2 * uint64(unsafe.Sizeof(AuditRecord{}))
	for _, tc := range []struct {
		name                   string
		graph, sources, audits uint64
	}{
		{name: "empty"},
		{name: "ordinary", graph: 7, sources: 3, audits: 5},
		{name: "largest graph workspace", graph: (maximum-4096)/1024 - 1},
		{name: "graph workspace overflow", graph: (maximum - 4096) / 1024},
		{name: "node count sum overflow", graph: maximum, sources: 1},
		{name: "candidate node overflow", graph: maximum},
		{name: "per-node witness overflow", sources: maximum/288 + 1},
		{name: "audit multiplication overflow", audits: maximum/auditBytes + 1},
		{name: "largest audit workspace", audits: (maximum - 4096 - 1024) / auditBytes},
		{name: "audit plus nodes overflow", audits: (maximum-4096-1024)/auditBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arbitrary precision evaluates the existing budget formula without
			// imposing machine overflow or constructing the synthetic objects.
			nodes := new(big.Int).Add(new(big.Int).SetUint64(tc.graph), new(big.Int).SetUint64(tc.sources))
			nodes.Add(nodes, big.NewInt(1))
			perNode := new(big.Int).Mul(new(big.Int).SetUint64(tc.sources), big.NewInt(288))
			perNode.Add(perNode, big.NewInt(1024))
			want := new(big.Int).Mul(nodes, perNode)
			want.Add(want, big.NewInt(4096))
			want.Add(want, new(big.Int).Mul(new(big.Int).SetUint64(tc.audits), new(big.Int).SetUint64(auditBytes)))
			got, err := headWorkspaceBytes(tc.graph, tc.sources, tc.audits)
			if !want.IsUint64() {
				if !errors.Is(err, ErrMemoryBudget) || got != 0 {
					t.Fatalf("unrepresentable workspace = %d, %v; exact bytes=%s", got, err, want)
				}
			} else if err != nil || got != want.Uint64() {
				t.Fatalf("workspace = %d, %v; want %s", got, err, want)
			}
		})
	}
}

func TestHeadWorkspaceRecomputesAfterIdleEviction(t *testing.T) {
	budget := NewMemoryBudget(6000)
	node := chainNode(1, 1, AnchorNode{})
	released := 0
	tracker := &HeadTracker{
		cfg: HeadTrackerConfig{Budget: budget},
		graph: map[[32]byte]*graphEntry{node.Hash: {
			node: node, release: func() { released++ },
		}},
	}
	release, err := tracker.reserveWork()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if released != 1 || len(tracker.graph) != 0 || budget.Used() != 5120 {
		t.Fatalf("idle eviction: released=%d graph=%d workspace=%d", released, len(tracker.graph), budget.Used())
	}
	release()
	release()
	if budget.Used() != 0 {
		t.Fatalf("workspace release retained %d bytes", budget.Used())
	}
}

func observeTip(t testing.TB, tr *HeadTracker, id string, seq uint64, n AnchorNode) {
	t.Helper()
	p, err := NewPayloadArena(tr.cfg.Budget).Store(n, 72)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Observe(TipObservation{Session: SourceSession{id, 1}, ReadSequence: seq, Node: n, Header: p}, time.Unix(0, int64(seq))); err != nil {
		t.Fatal(err)
	}
}

func commitReady(t testing.TB, tr *HeadTracker, q *EventQueue) Event {
	t.Helper()
	p, _, err := tr.Prepare(time.Unix(0, 100))
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("no prepared path")
	}
	if err := tr.Publish(q, p); err != nil {
		t.Fatal(err)
	}
	e, err := q.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTipsUseVerifiedCommonPrefixAndOneVotePerSource(t *testing.T) {
	tr, q, _ := newTracker(t, 8)
	n100 := chainNode(100, 1, AnchorNode{})
	n101 := chainNode(101, 1, n100)
	n102 := chainNode(102, 1, n101)
	n103 := chainNode(103, 1, n102)
	observeTip(t, tr, "a", 1, n103)
	observeTip(t, tr, "b", 1, n102)
	observeTip(t, tr, "c", 1, n101)
	e := commitReady(t, tr, q)
	if e.Delta.Appended[0].Node != n102 || len(e.Evaluation.Witnesses) != 2 {
		t.Fatalf("common prefix: %+v", e)
	}
	observeTip(t, tr, "a", 2, n101)
	p, _, err := tr.Prepare(time.Unix(0, 100))
	if err != nil || p != nil {
		t.Fatalf("ancestor support retracted committed tip: %v %v", p, err)
	}
	if tr.State().Tip != n102 {
		t.Fatal("ancestor support changed ledger")
	}
}

func TestUnknownInvalidChildCannotPoisonHealthyParentOrRecovery(t *testing.T) {
	tr, q, _ := newTracker(t, 8)
	parent := chainNode(100, 1, AnchorNode{})
	poison := chainNode(500, 9, parent)
	observeTip(t, tr, "c", 1, poison)
	observeTip(t, tr, "a", 1, parent)
	observeTip(t, tr, "b", 1, parent)
	e := commitReady(t, tr, q)
	if e.Delta.Appended[0].Node != parent || len(e.Evaluation.Witnesses) != 2 {
		t.Fatalf("invalid child poisoned healthy quorum: %+v", e)
	}
	p, r, err := tr.Prepare(time.Unix(0, 100))
	if err != nil || p != nil || r != nil {
		t.Fatalf("invalid known edge started recovery: %v %v %v", p, r, err)
	}
}

func TestHeadTrackerFixedModesAndStrictMajority(t *testing.T) {
	for _, tc := range []struct {
		members []string
		q       int
		mode    Mode
		valid   bool
		tier    EvidenceTier
	}{
		{[]string{"a"}, 1, StrictMode, false, Corroborated},
		{[]string{"a", "b", "c", "d"}, 2, StrictMode, false, Corroborated},
		{[]string{"a", "b", "c"}, 2, StrictMode, true, Corroborated},
		{[]string{"a"}, 1, SelectedMode, true, SingleSourceSelected},
		{[]string{"a"}, 1, ForwardedMode, true, Forwarded},
		{[]string{"a"}, 1, Mode(99), false, Corroborated},
	} {
		budget := NewMemoryBudget(1 << 20)
		tr, err := NewHeadTracker(HeadTrackerConfig{DomainID: "modes", Members: tc.members, Quorum: tc.q, Mode: tc.mode, RetainedNodes: 1, Budget: budget})
		if (err == nil) != tc.valid {
			t.Fatalf("mode=%v Q=%d N=%d: %v", tc.mode, tc.q, len(tc.members), err)
		}
		if tr != nil {
			if tr.Basis().Tier != tc.tier {
				t.Fatal("evidence tier upgraded")
			}
			tr.Close()
		}
		if budget.Used() != 0 {
			t.Fatal("open/close leaked memory")
		}
	}
}

func TestHeadTrackerRejectsOldSessionAndNonIncreasingReadSequence(t *testing.T) {
	tr, _, _ := newTracker(t, 8)
	n := chainNode(100, 1, AnchorNode{})
	observeTip(t, tr, "a", 2, n)
	if err := tr.Observe(TipObservation{Session: SourceSession{"a", 1}, ReadSequence: 2, Node: n}, time.Unix(0, 3)); !errors.Is(err, ErrReadSequence) {
		t.Fatalf("duplicate read sequence: %v", err)
	}
	if err := tr.Bind(SourceSession{"a", 2}, time.Unix(0, 4)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Observe(TipObservation{Session: SourceSession{"a", 1}, ReadSequence: 3, Node: n}, time.Unix(0, 5)); !errors.Is(err, ErrSessionStale) {
		t.Fatalf("old session accepted: %v", err)
	}
	if tr.tips["a"].valid {
		t.Fatal("reconnect inherited old support")
	}
}

func TestAnswersDoNotImplySupportAndLateTipReusesCache(t *testing.T) {
	tr, q, _ := newTracker(t, 8)
	x := chainNode(10, 1, AnchorNode{})
	y := chainNode(10, 2, AnchorNode{})
	observeTip(t, tr, "a", 1, x)
	observeTip(t, tr, "b", 1, y)
	eval, err := tr.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if got := eval.Target; got != nil {
		t.Fatalf("split answers admitted %+v", got)
	}
	observeTip(t, tr, "c", 1, y)
	e := commitReady(t, tr, q)
	if e.Delta.Appended[0].Node != y {
		t.Fatal("late source did not use B cache")
	}
}

func TestPreparedPathFailureDoesNotChangeLedgerOrAudit(t *testing.T) {
	tr, _, b := newTracker(t, 8)
	n := chainNode(10, 1, AnchorNode{})
	observeTip(t, tr, "a", 1, n)
	observeTip(t, tr, "b", 1, n)
	p, _, err := tr.Prepare(time.Unix(0, 100))
	if err != nil || p == nil {
		t.Fatalf("prepare %v %v", p, err)
	}
	before := tr.State()
	audit := tr.PendingAudit()
	q := NewEventQueue(NewMemoryBudget(1))
	if err := tr.Publish(q, p); !errors.Is(err, ErrMemoryBudget) {
		t.Fatalf("publish %v", err)
	}
	if tr.State() != before || tr.PendingAudit() != audit {
		t.Fatal("failed publication changed committed facts")
	}
	_ = b
}

func supplyObject(t testing.TB, tr *HeadTracker, r *ObjectRequest, node AnchorNode) {
	t.Helper()
	if r == nil {
		t.Fatal("missing request")
	}
	p, err := NewPayloadArena(tr.cfg.Budget).Store(node, 72)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.AcceptObject(ObjectResult{Request: *r, Session: SourceSession{"c", 1}, Node: node, Header: p}, time.Unix(0, 50)); err != nil {
		t.Fatal(err)
	}
}

func prepareWithObjects(t testing.TB, tr *HeadTracker, q *EventQueue, objects map[[32]byte]AnchorNode) Event {
	t.Helper()
	for range 100 {
		p, r, err := tr.Prepare(time.Unix(0, 100))
		if err != nil {
			t.Fatal(err)
		}
		if p != nil {
			if err := tr.Publish(q, p); err != nil {
				t.Fatal(err)
			}
			e, err := q.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			return e
		}
		if r == nil {
			t.Fatal("neither delta nor object request")
		}
		n, ok := objects[r.Hash]
		if !ok {
			t.Fatalf("missing fixture %x", r.Hash)
		}
		supplyObject(t, tr, r, n)
	}
	t.Fatal("recovery failed to converge")
	return Event{}
}

func TestPrunedDeliveredPathRetractsEveryDeliveredBlock(t *testing.T) {
	tr, q, _ := newTracker(t, 1)
	objects := map[[32]byte]AnchorNode{}
	n := chainNode(100, 1, AnchorNode{})
	objects[n.Hash] = n
	anchor := n
	for i := uint64(100); i <= 105; i++ {
		if i > 100 {
			n = chainNode(i, 1, n)
			objects[n.Hash] = n
		}
		observeTip(t, tr, "a", i, n)
		observeTip(t, tr, "b", i, n)
		commitReady(t, tr, q)
	}
	if tr.RetainedNodes() > 4 {
		t.Fatalf("cache did not prune: %d", tr.RetainedNodes())
	}
	alt := chainNode(101, 2, anchor)
	objects[alt.Hash] = alt
	observeTip(t, tr, "a", 106, alt)
	observeTip(t, tr, "b", 106, alt)
	e := prepareWithObjects(t, tr, q, objects)
	if len(e.Delta.Retracted) != 5 {
		t.Fatalf("retracted=%v", e.Delta.Retracted)
	}
	for i, n := range e.Delta.Retracted {
		if n.Number != 105-uint64(i) {
			t.Fatal("retraction order")
		}
	}
}

func TestPreBirthReplacementBecomesDeliveredFloorForLaterRetraction(t *testing.T) {
	tr, q, _ := newTracker(t, 2)
	objects := map[[32]byte]AnchorNode{}
	common := chainNode(98, 1, AnchorNode{})
	old99 := chainNode(99, 1, common)
	old100 := chainNode(100, 1, old99)
	for _, n := range []AnchorNode{common, old99, old100} {
		objects[n.Hash] = n
	}
	observeTip(t, tr, "a", 1, old100)
	observeTip(t, tr, "b", 1, old100)
	commitReady(t, tr, q)
	alt99 := chainNode(99, 2, common)
	alt100 := chainNode(100, 2, alt99)
	for _, n := range []AnchorNode{alt99, alt100} {
		objects[n.Hash] = n
	}
	observeTip(t, tr, "a", 2, alt100)
	observeTip(t, tr, "b", 2, alt100)
	e := prepareWithObjects(t, tr, q, objects)
	if e.Delta.Reason != PreBirthReorg || tr.State().DeliveredLowerBound != 99 {
		t.Fatal("pre-birth lower bound")
	}
	next99 := chainNode(99, 3, common)
	objects[next99.Hash] = next99
	observeTip(t, tr, "a", 3, next99)
	observeTip(t, tr, "b", 3, next99)
	e = prepareWithObjects(t, tr, q, objects)
	if len(e.Delta.Retracted) != 2 || e.Delta.Retracted[1] != alt99 || tr.State().BirthAnchor != old100 {
		t.Fatal("second pre-birth reorg missed delivered low node")
	}
}

func TestWitnessReplacementReusesRecoveryAndFreezesNewSnapshot(t *testing.T) {
	tr, q, _ := newTracker(t, 8)
	base := chainNode(100, 1, AnchorNode{})
	one := chainNode(101, 1, base)
	two := chainNode(102, 1, one)
	observeTip(t, tr, "a", 1, base)
	observeTip(t, tr, "b", 1, base)
	commitReady(t, tr, q)
	observeTip(t, tr, "a", 2, two)
	observeTip(t, tr, "b", 2, two)
	p, r, err := tr.Prepare(time.Unix(0, 20))
	if err != nil || p != nil || r == nil {
		t.Fatalf("recovery %v %v %v", p, r, err)
	}
	if err := tr.Disconnect(SourceSession{"b", 1}, time.Unix(0, 21)); err != nil {
		t.Fatal(err)
	}
	observeTip(t, tr, "c", 1, two)
	supplyObject(t, tr, r, one)
	e := commitReady(t, tr, q)
	if len(e.Evaluation.Witnesses) != 2 || e.Evaluation.Witnesses[1].Session.SourceID != "c" {
		t.Fatalf("stale witnesses %+v", e.Evaluation)
	}
	if e.Delta.Appended[0].ObjectSupplier.Session.SourceID != "c" {
		t.Fatal("object supplier missing")
	}
	found := false
	for a := range e.Audit.All() {
		if a.Kind == SessionDisconnected {
			found = true
		}
	}
	if !found {
		t.Fatal("witness change lost from audit")
	}
}

func TestUnknownJumpKeepsUsefulRecoveryWithoutKeepingOldVotes(t *testing.T) {
	tr, q, _ := newTracker(t, 1)
	objects := map[[32]byte]AnchorNode{}
	base := chainNode(100, 1, AnchorNode{})
	objects[base.Hash] = base
	n := base
	for i := uint64(101); i <= 107; i++ {
		n = chainNode(i, 1, n)
		objects[n.Hash] = n
	}
	var n105, n104, n107 AnchorNode
	for _, v := range objects {
		switch v.Number {
		case 104:
			n104 = v
		case 105:
			n105 = v
		case 107:
			n107 = v
		}
	}
	observeTip(t, tr, "a", 1, base)
	observeTip(t, tr, "b", 1, base)
	commitReady(t, tr, q)
	observeTip(t, tr, "a", 2, n105)
	observeTip(t, tr, "b", 2, n105)
	_, r, err := tr.Prepare(time.Unix(0, 10))
	if err != nil || r == nil {
		t.Fatal(err)
	}
	observeTip(t, tr, "a", 3, n107)
	observeTip(t, tr, "b", 3, n107)
	if !tr.RequestPending(*r) {
		t.Fatal("jump canceled useful recovery")
	}
	e, err := tr.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if e.Target == nil || *e.Target != n107 {
		t.Fatal("new tip did not replace old vote")
	}
	supplyObject(t, tr, r, n104)
	out := prepareWithObjects(t, tr, q, objects)
	if len(out.Delta.Appended) != 5 && len(out.Delta.Appended) != 7 {
		t.Fatalf("advance length=%d", len(out.Delta.Appended))
	}
}

func TestProvenForkReplacesRequestWithoutResettingRecoveryLimits(t *testing.T) {
	for _, limit := range []string{"time", "attempts"} {
		t.Run(limit, func(t *testing.T) {
			tr, q, _ := newTracker(t, 8)
			base := chainNode(100, 1, AnchorNode{})
			observeTip(t, tr, "a", 1, base)
			observeTip(t, tr, "b", 1, base)
			commitReady(t, tr, q)
			x104 := chainNode(104, 1, AnchorNode{})
			x105 := chainNode(105, 1, x104)
			y104 := chainNode(104, 2, AnchorNode{})
			y105 := chainNode(105, 2, y104)
			start := time.Unix(10, 0)
			tr.cfg.RecoveryTimeout = 10 * time.Second
			if limit == "attempts" {
				tr.cfg.MaxRecoveryAttempts = 2
			}
			observeTip(t, tr, "a", 2, x105)
			observeTip(t, tr, "b", 2, x105)
			_, first, err := tr.Prepare(start)
			if err != nil || first == nil || first.Hash != x104.Hash {
				t.Fatalf("first request %v %v", first, err)
			}
			observeTip(t, tr, "a", 3, y105)
			observeTip(t, tr, "b", 3, y105)
			_, replacement, err := tr.Prepare(start.Add(9 * time.Second))
			if err != nil || replacement == nil || replacement.Hash != y104.Hash || tr.RequestPending(*first) {
				t.Fatalf("proven useless request survived: %v %v", replacement, err)
			}
			if limit == "time" {
				err = tr.Tick(start.Add(10 * time.Second))
			} else {
				observeTip(t, tr, "a", 4, x105)
				observeTip(t, tr, "b", 4, x105)
				_, _, err = tr.Prepare(start.Add(9 * time.Second))
			}
			if !errors.Is(err, ErrRecoveryExhausted) {
				t.Fatalf("target churn reset %s limit: %v", limit, err)
			}
		})
	}
}

func TestAuditDeadlineUsesOldestFactAndNonObservationRecordsUseCurrentRevision(t *testing.T) {
	tr, _, _ := newTracker(t, 8)
	tr.cfg.AuditTimeout = 10 * time.Second
	first := chainNode(100, 1, AnchorNode{})
	observeTip(t, tr, "a", 1, first)
	start := time.Unix(0, 1)
	if err := tr.GateExpired(start.Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tr.audit(RequestFailed, SourceSession{"b", 1}, AnchorNode{}, AnchorNode{}, 9, start.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tr.audit(EvaluationReplaced, SourceSession{}, first, first, 0, start.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	for a := tr.auditFirst; a != nil; a = a.next {
		if a.record.ObservationRevision != tr.revision || a.record.EvaluationRevision > tr.evaluation {
			t.Fatalf("audit references a future snapshot: %+v revision=%d", a.record, tr.revision)
		}
	}
	if err := tr.Tick(start.Add(10 * time.Second)); !errors.Is(err, ErrAuditTimeout) {
		t.Fatalf("newer audit facts moved oldest deadline: %v", err)
	}
}

func TestLargeRecoveryShrinksGraphBackingAfterCommit(t *testing.T) {
	tr, q, budget := newTracker(t, 1)
	objects := make(map[[32]byte]AnchorNode)
	base := chainNode(100, 1, AnchorNode{})
	n := base
	for height := uint64(101); height <= 400; height++ {
		n = chainNode(height, 1, n)
		objects[n.Hash] = n
	}
	observeTip(t, tr, "a", 1, base)
	observeTip(t, tr, "b", 1, base)
	commitReady(t, tr, q)
	observeTip(t, tr, "a", 2, n)
	observeTip(t, tr, "b", 2, n)
	peakSlots := 0
	for {
		p, r, err := tr.Prepare(time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if tr.graphSlots > peakSlots {
			peakSlots = tr.graphSlots
		}
		if p != nil {
			if err := tr.Publish(q, p); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Next(context.Background()); err != nil {
				t.Fatal(err)
			}
			break
		}
		if r == nil {
			t.Fatal("recovery stopped")
		}
		supplyObject(t, tr, r, objects[r.Hash])
	}
	if peakSlots < 256 || tr.graphSlots != 16 || budget.Used() > 16000 {
		t.Fatalf("backing did not shrink: peak=%d slots=%d bytes=%d", peakSlots, tr.graphSlots, budget.Used())
	}
}

func TestContinuousAdvanceRetentionPlateausWithSlowSource(t *testing.T) {
	tr, q, b := newTracker(t, 8)
	n := chainNode(1, 1, AnchorNode{})
	observeTip(t, tr, "c", 1, n)
	var peak uint64
	for i := uint64(1); i <= 3000; i++ {
		if i > 1 {
			n = chainNode(i, 1, n)
		}
		observeTip(t, tr, "a", i, n)
		observeTip(t, tr, "b", i, n)
		commitReady(t, tr, q)
		if i > 100 && b.Used() > peak {
			peak = b.Used()
		}
		if tr.RetainedNodes() > 12 {
			t.Fatalf("unbounded nodes %d at %d", tr.RetainedNodes(), i)
		}
		if b.Used() > 20000 {
			t.Fatalf("unbounded memory %d at %d", b.Used(), i)
		}
	}
	t.Logf("retained peak=%d bytes", peak)
}

func TestSlowSourceNeverTriggersUselessBackwardRecovery(t *testing.T) {
	tr, q, _ := newTracker(t, 1)
	n := chainNode(100, 1, AnchorNode{})
	observeTip(t, tr, "a", 100, n)
	observeTip(t, tr, "b", 100, n)
	commitReady(t, tr, q)
	observeTip(t, tr, "c", 1, chainNode(1, 7, AnchorNode{}))
	for i := uint64(101); i < 500; i++ {
		if i > 100 {
			n = chainNode(i, 1, n)
		}
		for _, id := range []string{"a", "b"} {
			observeTip(t, tr, id, i, n)
			p, r, err := tr.Prepare(time.Unix(0, 100))
			if err != nil {
				t.Fatal(err)
			}
			if r != nil {
				t.Fatalf("healthy sources triggered old-source lookup at %d: %+v", i, r)
			}
			if p != nil {
				if err := tr.Publish(q, p); err != nil {
					t.Fatal(err)
				}
				if _, err := q.Next(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestHigherTipsDoNotStarveCompleteRecoveringTarget(t *testing.T) {
	tr, q, _ := newTracker(t, 1)
	objects := map[[32]byte]AnchorNode{}
	n := chainNode(100, 1, AnchorNode{})
	objects[n.Hash] = n
	observeTip(t, tr, "a", 1, n)
	observeTip(t, tr, "b", 1, n)
	commitReady(t, tr, q)
	for i := uint64(101); i <= 110; i++ {
		n = chainNode(i, 1, n)
		objects[n.Hash] = n
	}
	lookup := func(number uint64) AnchorNode {
		for _, v := range objects {
			if v.Number == number {
				return v
			}
		}
		t.Fatal("fixture")
		return AnchorNode{}
	}
	observeTip(t, tr, "a", 2, lookup(105))
	observeTip(t, tr, "b", 2, lookup(105))
	_, r, err := tr.Prepare(time.Unix(0, 10))
	if err != nil || r == nil {
		t.Fatal(err)
	}
	for i := uint64(106); i <= 110; i++ {
		observeTip(t, tr, "a", i, lookup(i))
		observeTip(t, tr, "b", i, lookup(i))
	}
	supplyObject(t, tr, r, lookup(104))
	out := prepareWithObjects(t, tr, q, objects)
	if out.Delta.Appended[len(out.Delta.Appended)-1].Node.Number != 105 {
		t.Fatal("recovering target was starved by higher tips")
	}
	for _, w := range out.Evaluation.Witnesses {
		if w.Tip.Number != 110 || len(w.Path) != 6 {
			t.Fatal("high tip proof missing")
		}
	}
	proof := append([]AnchorNode(nil), out.Evaluation.Witnesses[0].Path...)
	prepareWithObjects(t, tr, q, objects)
	for i, n := range proof {
		if out.Evaluation.Witnesses[0].Path[i] != n {
			t.Fatal("delivered witness mutated by cache pruning")
		}
	}
}

func TestLowerForkFindsQuorumAfterLosingOldEvaluation(t *testing.T) {
	tr, q, _ := newTracker(t, 8)
	objects := map[[32]byte]AnchorNode{}
	common := chainNode(99, 1, AnchorNode{})
	old := common
	for i := uint64(100); i <= 105; i++ {
		old = chainNode(i, 1, old)
		objects[old.Hash] = old
	}
	objects[common.Hash] = common
	observeTip(t, tr, "a", 1, old)
	observeTip(t, tr, "b", 1, old)
	commitReady(t, tr, q)
	low := chainNode(100, 2, common)
	mid := chainNode(101, 2, low)
	high := chainNode(102, 2, mid)
	for _, n := range []AnchorNode{low, mid, high} {
		objects[n.Hash] = n
	}
	observeTip(t, tr, "a", 2, high)
	observeTip(t, tr, "b", 2, low)
	e := prepareWithObjects(t, tr, q, objects)
	if e.Delta.Appended[len(e.Delta.Appended)-1].Node != low {
		t.Fatal("lower fork could not gain support")
	}
}

func TestAuditPrefixAndTerminalTransferKeepExactOwnership(t *testing.T) {
	tr, q, b := newTracker(t, 4)
	n := chainNode(10, 1, AnchorNode{})
	observeTip(t, tr, "a", 1, n)
	observeTip(t, tr, "b", 1, n)
	p, _, err := tr.Prepare(time.Unix(0, 3))
	if err != nil {
		t.Fatal(err)
	}
	last := p.Event.Audit.Last
	if err := tr.GateExpired(time.Unix(0, 4)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Publish(q, p); err != nil {
		t.Fatal(err)
	}
	event, err := q.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Audit.Last != last || tr.PendingAudit() != 1 {
		t.Fatal("commit consumed audit beyond prepared prefix")
	}
	count := 0
	for range event.Audit.All() {
		count++
	}
	if count != 2 {
		t.Fatal("old event reaches new audit suffix")
	}
	terminal := tr.Terminal(ErrAuditTimeout)
	tr.Close()
	if b.Used() == 0 {
		t.Fatal("undelivered terminal lost reservations")
	}
	if err := q.Close(terminal); err != nil {
		t.Fatal(err)
	}
	_, err = q.Next(context.Background())
	var term *TerminalError
	if !errors.As(err, &term) || term.Audit.First != last+1 {
		t.Fatalf("terminal %v", err)
	}
	if b.Used() != 0 {
		t.Fatal("terminal did not transfer budget")
	}
	if q.terminal != nil {
		t.Fatal("queue retained delivered terminal")
	}
}

func TestTTLAndObjectLookupNeverRefreshSourceTip(t *testing.T) {
	tr, q, _ := newTracker(t, 4)
	tr.cfg.TipTTL = 10 * time.Nanosecond
	n := chainNode(10, 1, AnchorNode{})
	observeTip(t, tr, "a", 1, n)
	observeTip(t, tr, "b", 1, n)
	p, _, err := tr.Prepare(time.Unix(0, 2))
	if err != nil || p == nil {
		t.Fatal(err)
	}
	if err := tr.Publish(q, p); err != nil {
		t.Fatal(err)
	}
	_, _ = q.Next(context.Background())
	if err := tr.Tick(time.Unix(0, 12)); err != nil {
		t.Fatal(err)
	}
	e, err := tr.Evaluate()
	if err != nil || e.Target != nil {
		t.Fatal("expired source still supports")
	}
	if tr.State().Tip != n {
		t.Fatal("TTL retracted ledger")
	}
}

func TestRetryDeadlineCannotBeBypassedByNewTips(t *testing.T) {
	tr, q, _ := newTracker(t, 4)
	n := chainNode(100, 1, AnchorNode{})
	one := chainNode(101, 1, n)
	two := chainNode(102, 1, one)
	observeTip(t, tr, "a", 1, n)
	observeTip(t, tr, "b", 1, n)
	commitReady(t, tr, q)
	observeTip(t, tr, "a", 2, two)
	observeTip(t, tr, "b", 2, two)
	_, r, err := tr.Prepare(time.Unix(0, 20))
	if err != nil || r == nil {
		t.Fatal(err)
	}
	if err := tr.ExhaustRequest(*r, time.Unix(0, 20), time.Second); err != nil {
		t.Fatal(err)
	}
	observeTip(t, tr, "c", 1, two)
	_, r, err = tr.Prepare(time.Unix(0, 21))
	if err != nil || r != nil {
		t.Fatal("new observation bypassed retry delay")
	}
}
