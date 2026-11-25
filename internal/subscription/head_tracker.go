package subscription

import (
	"fmt"
	"time"
	"unsafe"
)

// HeadTracker has one serial owner. Time and validated objects are explicit
// inputs; it never performs IO, creates goroutines, or reads the clock.
type HeadTrackerConfig struct {
	DomainID                              string
	Members                               []string
	Quorum                                int
	Mode                                  Mode
	RetainedNodes                         int
	Budget                                MemoryBudget
	TipTTL, RecoveryTimeout, AuditTimeout time.Duration
	MaxRecoveryAttempts                   uint64
}
type TipObservation struct {
	Session      SourceSession
	ReadSequence uint64
	Node         AnchorNode
	Header       PayloadLease
}
type sourceTip struct {
	session            SourceSession
	sequence, revision uint64
	node               AnchorNode
	valid              bool
	connected          bool
	firstAt            time.Time
	at                 time.Time
}
type graphEntry struct {
	node     AnchorNode
	header   PayloadLease
	supplier ObjectSupplier
	release  func()
	touched  uint64
	pinned   bool
}
type auditEntry struct {
	record  AuditRecord
	at      time.Time
	release func()
	next    *auditEntry
}
type HeadState struct {
	Tip, BirthAnchor                 AnchorNode
	PathVersion, DeliveredLowerBound uint64
}
type PreparedDelta struct {
	Event       Event
	observation uint64
	lastAudit   *auditEntry
	release     func()
}
type HeadTracker struct {
	cfg                                                           HeadTrackerConfig
	tips                                                          map[string]*sourceTip
	graph                                                         map[[32]byte]*graphEntry
	graphSlots                                                    int
	graphRelease                                                  func()
	state                                                         HeadState
	revision, evaluation, auditSequence, requestSequence, touches uint64
	auditFirst, auditLast                                         *auditEntry
	auditCount                                                    int
	request                                                       *ObjectRequest
	requestRelease                                                func()
	retryAt                                                       time.Time
	recoveryStart                                                 time.Time
	recoveryAttempts                                              uint64
	pending                                                       *AnchorNode
	lastEvaluation                                                *AnchorNode
	fixedRelease                                                  func()
	terminalRelease                                               func()
	closed                                                        bool
}

func NewHeadTracker(cfg HeadTrackerConfig) (*HeadTracker, error) {
	if cfg.DomainID == "" || len(cfg.Members) == 0 || cfg.RetainedNodes <= 0 || cfg.Budget == nil {
		return nil, fmt.Errorf("subscription: domain, members, positive retention and budget required")
	}
	if cfg.Mode != StrictMode && cfg.Mode != SelectedMode && cfg.Mode != ForwardedMode {
		return nil, fmt.Errorf("subscription: invalid head evidence mode")
	}
	if cfg.Quorum < 1 || cfg.Quorum > len(cfg.Members) || (cfg.Mode == StrictMode && (cfg.Quorum < 2 || 2*cfg.Quorum <= len(cfg.Members))) {
		return nil, fmt.Errorf("subscription: strict majority required")
	}
	if cfg.Mode != StrictMode {
		cfg.Quorum = 1
	}
	if cfg.TipTTL < 0 || cfg.RecoveryTimeout < 0 || cfg.AuditTimeout < 0 {
		return nil, fmt.Errorf("subscription: negative timeout")
	}
	// Fixed slots, map overhead and terminal control space are admitted at Open.
	bytes := uint64(unsafe.Sizeof(HeadTracker{}))
	for _, id := range cfg.Members {
		bytes += uint64(len(id)) + uint64(unsafe.Sizeof(sourceTip{})) + 192
	}
	release, err := cfg.Budget.Reserve(bytes)
	if err != nil {
		return nil, err
	}
	terminal, err := cfg.Budget.Reserve(512)
	if err != nil {
		release()
		return nil, err
	}
	h := &HeadTracker{cfg: cfg, tips: make(map[string]*sourceTip, len(cfg.Members)), fixedRelease: release, terminalRelease: terminal}
	h.cfg.Members = append([]string(nil), cfg.Members...)
	for _, id := range cfg.Members {
		if id == "" || h.tips[id] != nil {
			release()
			terminal()
			return nil, ErrUnknownSource
		}
		h.tips[id] = &sourceTip{}
	}
	return h, nil
}
func (h *HeadTracker) State() HeadState { return h.state }
func (h *HeadTracker) Basis() EvidenceBasis {
	return EvidenceBasis{DomainID: h.cfg.DomainID, Members: append([]string(nil), h.cfg.Members...), Quorum: h.cfg.Quorum, Tier: h.tier()}
}
func (h *HeadTracker) PendingAudit() int { return h.auditCount }
func (h *HeadTracker) ArrivalSample(n AnchorNode) (time.Duration, int) {
	var first, last time.Time
	count := 0
	for _, t := range h.tips {
		if t.valid && t.node.Hash == n.Hash {
			count++
			if first.IsZero() || t.firstAt.Before(first) {
				first = t.firstAt
			}
			if last.IsZero() || t.firstAt.After(last) {
				last = t.firstAt
			}
		}
	}
	return last.Sub(first), count
}
func (h *HeadTracker) RetainedNodes() int { return len(h.graph) }
func (h *HeadTracker) ActiveRequests() int {
	if h.request != nil {
		return 1
	}
	return 0
}
func (h *HeadTracker) session(s SourceSession) (*sourceTip, error) {
	if h.closed {
		return nil, ErrClosed
	}
	t := h.tips[s.SourceID]
	if t == nil {
		return nil, ErrUnknownSource
	}
	if s.Generation == 0 || t.session != s || !t.connected {
		return nil, ErrSessionStale
	}
	return t, nil
}
func (h *HeadTracker) audit(kind AuditKind, s SourceSession, old, next AnchorNode, request uint64, now time.Time, reasons ...AuditReason) error {
	release, err := h.cfg.Budget.Reserve(uint64(unsafe.Sizeof(auditEntry{})) + 32)
	if err != nil {
		return err
	}
	h.auditSequence++
	observation := h.revision
	if kind == TipChanged || kind == TipInvalidated || kind == SessionDisconnected {
		observation++
	}
	a := &auditEntry{record: AuditRecord{Sequence: h.auditSequence, ObservationRevision: observation, EvaluationRevision: h.evaluation, RequestID: request, Session: s, Previous: old, Current: next, Kind: kind, At: now.UnixNano()}, at: now, release: release}
	switch kind {
	case TipInvalidated:
		a.record.Reason = ReasonInvalidPush
	case SessionDisconnected:
		a.record.Reason = ReasonDisconnected
	case RequestFailed:
		a.record.Reason = ReasonRequestFailure
	case InvalidObject:
		a.record.Reason = ReasonInvalidObject
	case EvaluationReplaced:
		a.record.Reason = ReasonTargetChanged
	case WaitExpired:
		a.record.Reason = ReasonGateExpired
	}
	if len(reasons) > 0 {
		a.record.Reason = reasons[0]
	}
	if h.auditLast == nil {
		h.auditFirst = a
	} else {
		h.auditLast.next = a
	}
	h.auditLast = a
	h.auditCount++
	return nil
}
func (h *HeadTracker) Bind(s SourceSession, now time.Time) error {
	if h.closed {
		return ErrClosed
	}
	t := h.tips[s.SourceID]
	if t == nil {
		return ErrUnknownSource
	}
	if s.Generation == 0 || s.Generation <= t.session.Generation {
		return ErrSessionStale
	}
	if t.session.Generation != 0 {
		if err := h.audit(TipInvalidated, t.session, t.node, AnchorNode{}, 0, now, ReasonReconnect); err != nil {
			return err
		}
	}
	*t = sourceTip{session: s, connected: true}
	h.revision++
	return nil
}
func (h *HeadTracker) Disconnect(s SourceSession, now time.Time, reason ...AuditReason) error {
	t, err := h.session(s)
	if err != nil {
		return err
	}
	if err = h.audit(SessionDisconnected, s, t.node, AnchorNode{}, 0, now, reason...); err != nil {
		return err
	}
	t.valid = false
	t.connected = false
	h.revision++
	h.prune()
	return nil
}
func (h *HeadTracker) Reject(s SourceSession, seq uint64, now time.Time, reason ...AuditReason) error {
	t, err := h.session(s)
	if err != nil {
		return err
	}
	if seq <= t.sequence {
		return ErrReadSequence
	}
	if err = h.audit(TipInvalidated, s, t.node, AnchorNode{}, 0, now, reason...); err != nil {
		return err
	}
	t.valid = false
	t.sequence = seq
	h.revision++
	h.prune()
	return nil
}
func (h *HeadTracker) Observe(o TipObservation, now time.Time) error {
	owned := true
	defer func() {
		if owned {
			o.Header.Release()
		}
	}()
	t, err := h.session(o.Session)
	if err != nil {
		return err
	}
	if o.ReadSequence <= t.sequence {
		return ErrReadSequence
	}
	if o.Node.Hash == ([32]byte{}) {
		return ErrInvalidCandidate
	}
	if err = h.validateNode(o.Node); err != nil {
		return err
	}
	if t.valid && t.node == o.Node {
		t.sequence = o.ReadSequence
		t.at = now
		return nil
	}
	// Reserve the DAG entry before recording the accepted fact. A failed audit
	// admission leaves the source slot untouched and discards the new object.
	previous := h.graph[o.Node.Hash]
	addedHeader := previous != nil && previous.header.state == nil
	entry, created, err := h.insert(o.Node, o.Header, ObjectSupplier{Session: o.Session, Acquisition: Push, TargetHash: o.Node.Hash})
	if err != nil {
		return err
	}
	owned = false
	if err = h.audit(TipChanged, o.Session, t.node, o.Node, 0, now); err != nil {
		if created {
			h.remove(entry.node.Hash)
		} else if addedHeader {
			entry.header.Release()
			entry.header = PayloadLease{}
			entry.supplier = ObjectSupplier{}
		}
		return err
	}
	h.revision++
	t.node = o.Node
	t.valid = true
	t.sequence = o.ReadSequence
	t.revision = h.revision
	t.at = now
	t.firstAt = now
	h.prune()
	return nil
}
func (h *HeadTracker) validateNode(n AnchorNode) error {
	if n.Hash == ([32]byte{}) {
		return ErrInvalidCandidate
	}
	if e := h.graph[n.Hash]; e != nil && e.node != n {
		return ErrInvalidCandidate
	}
	if p := h.graph[n.ParentHash]; p != nil && (n.Number == 0 || p.node.Number != n.Number-1) {
		return ErrInvalidCandidate
	}
	// An older unverified child cannot veto a hash-valid parent. Every
	// traversal checks the edge's height before it counts as support.
	return nil
}
func (h *HeadTracker) insert(n AnchorNode, p PayloadLease, s ObjectSupplier) (*graphEntry, bool, error) {
	if e := h.graph[n.Hash]; e != nil {
		if e.header.state == nil {
			e.header = p
			e.supplier = s
		} else {
			p.Release()
		}
		h.touches++
		e.touched = h.touches
		return e, false, nil
	}
	release, err := h.cfg.Budget.Reserve(uint64(unsafe.Sizeof(graphEntry{})) + 160)
	if err != nil {
		h.evictIdle()
		release, err = h.cfg.Budget.Reserve(uint64(unsafe.Sizeof(graphEntry{})) + 160)
	}
	if err != nil {
		return nil, false, err
	}
	if len(h.graph) == h.graphSlots {
		if err := h.resizeGraph(len(h.graph) + 1); err != nil {
			release()
			return nil, false, err
		}
	}
	h.touches++
	e := &graphEntry{node: n, header: p, supplier: s, release: release, touched: h.touches}
	h.graph[n.Hash] = e
	return e, true, nil
}

// A Go map keeps its bucket storage after deletions. Reserve that storage
// separately and replace the table after a large contraction. Both old and new
// tables stay charged during the copy; if admission fails, retain the charged
// old table and retry on a later prune.
func (h *HeadTracker) resizeGraph(size int) error {
	slots := 16
	for slots < size {
		if slots > int(^uint(0)>>1)/2 {
			return ErrMemoryBudget
		}
		slots *= 2
	}
	if slots == h.graphSlots {
		return nil
	}
	if uint64(slots) > (^uint64(0)-128)/96 {
		return ErrMemoryBudget
	}
	release, err := h.cfg.Budget.Reserve(uint64(slots)*96 + 128)
	if err != nil {
		return err
	}
	graph := make(map[[32]byte]*graphEntry, slots)
	for hash, entry := range h.graph {
		graph[hash] = entry
	}
	if h.graphRelease != nil {
		h.graphRelease()
	}
	h.graph, h.graphSlots, h.graphRelease = graph, slots, release
	return nil
}
func (h *HeadTracker) remove(hash [32]byte) {
	if e := h.graph[hash]; e != nil {
		e.header.Release()
		e.release()
		delete(h.graph, hash)
	}
}
func (h *HeadTracker) path(tip AnchorNode, stop [32]byte) ([]AnchorNode, bool) {
	var out []AnchorNode
	for {
		out = append(out, tip)
		if tip.Hash == stop {
			return out, true
		}
		if tip.Number == 0 {
			return out, false
		}
		p := h.graph[tip.ParentHash]
		if p == nil || p.node.Number != tip.Number-1 {
			return out, false
		}
		tip = p.node
	}
}

// Evaluate returns a caller-owned diagnostic snapshot. Production Prepare
// keeps the same workspace reservation until Publish or Discard transfers it.
func (h *HeadTracker) Evaluate() (EvaluationSnapshot, error) {
	release, err := h.reserveWork()
	if err != nil {
		return EvaluationSnapshot{}, err
	}
	defer release()
	return h.evaluate(), nil
}
func (h *HeadTracker) evaluate() EvaluationSnapshot {
	h.evaluation++
	counts := make(map[[32]byte]int)
	nodes := make(map[[32]byte]AnchorNode)
	for _, id := range h.cfg.Members {
		t := h.tips[id]
		if !t.valid {
			continue
		}
		path, _ := h.path(t.node, [32]byte{})
		for _, n := range path {
			counts[n.Hash]++
			nodes[n.Hash] = n
		}
	}
	e := EvaluationSnapshot{Revision: h.evaluation, ObservationRevision: h.revision}
	for hash, count := range counts {
		if count >= h.cfg.Quorum && (e.Target == nil || nodes[hash].Number > e.Target.Number) {
			n := nodes[hash]
			e.Target = &n
		}
	}
	if e.Target == nil {
		return e
	}
	for _, id := range h.cfg.Members {
		t := h.tips[id]
		if !t.valid {
			continue
		}
		path, ok := h.path(t.node, e.Target.Hash)
		if ok {
			e.Witnesses = append(e.Witnesses, PathWitness{Session: t.session, ReadSequence: t.sequence, ObservationRevision: t.revision, EvaluationRevision: e.Revision, Tip: t.node, Path: path})
		}
	}
	return e
}
func (h *HeadTracker) Tick(now time.Time) error {
	if h.closed {
		return ErrClosed
	}
	for _, id := range h.cfg.Members {
		t := h.tips[id]
		if t.valid && h.cfg.TipTTL > 0 && !now.Before(t.at.Add(h.cfg.TipTTL)) {
			if err := h.audit(TipInvalidated, t.session, t.node, AnchorNode{}, 0, now, ReasonTipExpired); err != nil {
				return err
			}
			t.valid = false
			h.revision++
		}
	}
	if h.auditFirst != nil && h.cfg.AuditTimeout > 0 && !now.Before(h.auditFirst.at.Add(h.cfg.AuditTimeout)) {
		return ErrAuditTimeout
	}
	if !h.recoveryStart.IsZero() && h.cfg.RecoveryTimeout > 0 && !now.Before(h.recoveryStart.Add(h.cfg.RecoveryTimeout)) {
		return ErrRecoveryExhausted
	}
	h.prune()
	return nil
}
func (h *HeadTracker) NextDeadline() time.Time {
	var at time.Time
	include := func(t time.Time) {
		if at.IsZero() || t.Before(at) {
			at = t
		}
	}
	if !h.retryAt.IsZero() {
		include(h.retryAt)
	}
	if h.auditFirst != nil && h.cfg.AuditTimeout > 0 {
		include(h.auditFirst.at.Add(h.cfg.AuditTimeout))
	}
	if !h.recoveryStart.IsZero() && h.cfg.RecoveryTimeout > 0 {
		include(h.recoveryStart.Add(h.cfg.RecoveryTimeout))
	}
	if h.cfg.TipTTL > 0 {
		for _, t := range h.tips {
			if t.valid {
				include(t.at.Add(h.cfg.TipTTL))
			}
		}
	}
	return at
}
func (h *HeadTracker) GateExpired(now time.Time) error {
	if h.auditFirst == nil {
		return nil
	}
	return h.audit(WaitExpired, SourceSession{}, AnchorNode{}, AnchorNode{}, 0, now)
}
func (h *HeadTracker) need(n AnchorNode, now time.Time) (*ObjectRequest, error) {
	if !h.retryAt.IsZero() {
		if now.Before(h.retryAt) {
			return nil, nil
		}
		h.retryAt = time.Time{}
	}
	if h.request != nil {
		return nil, nil
	}
	if h.cfg.MaxRecoveryAttempts > 0 && h.recoveryAttempts >= h.cfg.MaxRecoveryAttempts {
		return nil, ErrRecoveryExhausted
	}
	release, err := h.cfg.Budget.Reserve(uint64(unsafe.Sizeof(ObjectRequest{})) + 128)
	if err != nil {
		return nil, err
	}
	if h.recoveryStart.IsZero() {
		h.recoveryStart = now
	}
	h.recoveryAttempts++
	h.requestSequence++
	r := ObjectRequest{ID: h.requestSequence, Hash: n.Hash, Number: n.Number}
	h.request = &r
	h.requestRelease = release
	return &r, nil
}
func (h *HeadTracker) missingParent(n AnchorNode, now time.Time) (*ObjectRequest, error) {
	if n.Number == 0 {
		return nil, ErrRecoveryExhausted
	}
	return h.need(AnchorNode{Hash: n.ParentHash, Number: n.Number - 1}, now)
}
func (h *HeadTracker) AcceptObject(r ObjectResult, now time.Time) error {
	owned := true
	defer func() {
		if owned {
			r.Header.Release()
		}
	}()
	if h.request == nil || *h.request != r.Request {
		return ErrStaleAttempt
	}
	if _, err := h.session(r.Session); err != nil {
		return err
	}
	if r.Err != nil {
		return h.audit(RequestFailed, r.Session, AnchorNode{}, AnchorNode{}, r.Request.ID, now, r.Reason)
	}
	if r.Node.Hash != r.Request.Hash || r.Node.Number != r.Request.Number {
		return h.audit(InvalidObject, r.Session, AnchorNode{}, r.Node, r.Request.ID, now, ReasonWrongTarget)
	}
	if err := h.validateNode(r.Node); err != nil {
		return h.audit(InvalidObject, r.Session, AnchorNode{}, r.Node, r.Request.ID, now)
	}
	if _, _, err := h.insert(r.Node, r.Header, ObjectSupplier{Session: r.Session, Acquisition: ByHash, RequestID: r.Request.ID, TargetHash: r.Request.Hash}); err != nil {
		return err
	}
	owned = false
	h.request = nil
	h.requestRelease()
	h.requestRelease = nil
	h.revision++
	return nil
}
func (h *HeadTracker) RequestPending(r ObjectRequest) bool {
	return h.request != nil && *h.request == r
}
func (h *HeadTracker) ExhaustRequest(r ObjectRequest, now time.Time, retryDelay time.Duration) error {
	if !h.RequestPending(r) {
		return nil
	}
	h.request = nil
	h.requestRelease()
	h.requestRelease = nil
	if retryDelay <= 0 {
		return ErrRecoveryExhausted
	}
	h.retryAt = now.Add(retryDelay)
	return nil
}

// Prepare freezes the evaluation and audit prefix while leaving the committed
// ledger untouched. It may request a missing object and be retried unchanged.
func (h *HeadTracker) Prepare(now time.Time) (*PreparedDelta, *ObjectRequest, error) {
	if err := h.Tick(now); err != nil {
		return nil, nil, err
	}
	work, err := h.reserveWork()
	if err != nil {
		return nil, nil, err
	}
	retained := false
	defer func() {
		if !retained {
			work()
		}
	}()
	e := h.evaluate()
	// An already recovering target may commit before a newer same-branch tip.
	// Rebuild its witnesses from current slots; the former votes are never kept.
	if h.pending != nil && e.Target != nil {
		witnesses := h.witnesses(*h.pending, e.Revision)
		if len(witnesses) >= h.cfg.Quorum {
			n := *h.pending
			e.Target = &n
			e.Witnesses = witnesses
		}
	}
	if e.Target != nil && h.lastEvaluation != nil && *e.Target != *h.lastEvaluation {
		if err := h.audit(EvaluationReplaced, SourceSession{}, *h.lastEvaluation, *e.Target, 0, now); err != nil {
			return nil, nil, err
		}
	}
	if e.Target != nil {
		n := *e.Target
		h.lastEvaluation = &n
	}
	target := e.Target
	if target == nil {
		target = h.pending
	}
	if target == nil {
		r, err := h.seekSupport(now, e.Target)
		return nil, r, err
	}
	h.cancelUselessRequest(*target)
	d, missing, err := h.buildDelta(*target)
	if err != nil {
		return nil, nil, err
	}
	if missing != nil {
		n := *target
		h.pending = &n
		r, err := h.need(*missing, now)
		return nil, r, err
	}
	if d == nil || e.Target == nil {
		r, err := h.seekSupport(now, e.Target)
		return nil, r, err
	}
	// All complete objects are present before the delta can become visible.
	for i := range d.Appended {
		a := &d.Appended[i]
		g := h.graph[a.Node.Hash]
		if g == nil || g.header.state == nil {
			n := *target
			h.pending = &n
			r, err := h.need(a.Node, now)
			return nil, r, err
		}
		a.Header = g.header
		a.ObjectSupplier = g.supplier
	}
	audit := AuditRange{first: h.auditFirst}
	if h.auditFirst != nil {
		audit.First = h.auditFirst.record.Sequence
		audit.Last = h.auditLast.record.Sequence
	}
	event := Event{Delta: d, Evaluation: e, Audit: audit, EvidenceTier: h.tier()}
	retained = true
	return &PreparedDelta{Event: event, observation: h.revision, lastAudit: h.auditLast, release: work}, nil, nil
}

func (h *HeadTracker) cancelUselessRequest(target AnchorNode) {
	if h.request == nil || h.request.Number <= h.state.Tip.Number {
		return // An object needed by the old committed branch remains useful.
	}
	r := h.request
	n := target
	for n.Number > r.Number {
		if n.Number == r.Number+1 {
			if n.ParentHash == r.Hash {
				return
			}
			break
		}
		p := h.graph[n.ParentHash]
		if p == nil || p.node.Number != n.Number-1 {
			return // A jump with unknown ancestry does not prove irrelevance.
		}
		n = p.node
	}
	if n.Number == r.Number && n.Hash == r.Hash {
		return
	}
	h.request = nil
	h.requestRelease()
	h.requestRelease = nil
}

// The finite graph and source-slot counts bound every map, temporary branch,
// append growth, and independent witness proof built by one evaluation. This
// conservative workspace reservation precedes those allocations.
func (h *HeadTracker) reserveWork() (func(), error) {
	bytes, err := headWorkspaceBytes(uint64(len(h.graph)), uint64(len(h.tips)), uint64(h.auditCount))
	if err != nil {
		return nil, err
	}
	release, err := h.cfg.Budget.Reserve(bytes)
	if err != nil {
		h.evictIdle()
		bytes, err = headWorkspaceBytes(uint64(len(h.graph)), uint64(len(h.tips)), uint64(h.auditCount))
		if err != nil {
			return nil, err
		}
		release, err = h.cfg.Budget.Reserve(bytes)
	}
	return release, err
}

func headWorkspaceBytes(graph, sources, audits uint64) (uint64, error) {
	const (
		controlBytes = uint64(4096)                             // Evaluation and delta control space.
		nodeBytes    = uint64(1024)                             // Maps, temporary branches and append growth per node.
		witnessBytes = uint64(288)                              // Independent path proof per source and node.
		auditBytes   = 2 * uint64(unsafe.Sizeof(AuditRecord{})) // Snapshot and event copies per retained audit record.
	)
	if graph > ^uint64(0)-sources {
		return 0, ErrMemoryBudget
	}
	nodes := graph + sources
	if nodes == ^uint64(0) {
		return 0, ErrMemoryBudget
	}
	nodes++ // One extra node covers the candidate being evaluated.
	if sources > (^uint64(0)-nodeBytes)/witnessBytes {
		return 0, ErrMemoryBudget
	}
	perNode := nodeBytes + sources*witnessBytes
	if audits > (^uint64(0)-controlBytes)/auditBytes {
		return 0, ErrMemoryBudget
	}
	bytes := controlBytes + audits*auditBytes
	if nodes > (^uint64(0)-bytes)/perNode {
		return 0, ErrMemoryBudget
	}
	return bytes + nodes*perNode, nil
}

func (h *HeadTracker) witnesses(target AnchorNode, revision uint64) []PathWitness {
	var out []PathWitness
	for _, id := range h.cfg.Members {
		t := h.tips[id]
		if !t.valid {
			continue
		}
		path, ok := h.path(t.node, target.Hash)
		if ok {
			out = append(out, PathWitness{Session: t.session, ReadSequence: t.sequence, ObservationRevision: t.revision, EvaluationRevision: revision, Tip: t.node, Path: path})
		}
	}
	return out
}
func (h *HeadTracker) tier() EvidenceTier {
	if h.cfg.Mode == StrictMode {
		return Corroborated
	}
	if h.cfg.Mode == SelectedMode {
		return SingleSourceSelected
	}
	return Forwarded
}
func (h *HeadTracker) seekSupport(now time.Time, current *AnchorNode) (*ObjectRequest, error) {
	if h.request != nil {
		return nil, nil
	}
	valid := 0
	for _, t := range h.tips {
		if t.valid {
			valid++
		}
	}
	if valid < h.cfg.Quorum {
		return nil, nil
	}
	// Recover one frontier at a time. Current source slots are never replaced by
	// these historical objects. Prefer the highest frontier for useful progress.
	var frontier *AnchorNode
	stop := h.state.Tip.Hash
	if current != nil {
		stop = current.Hash
	}
	for _, id := range h.cfg.Members {
		t := h.tips[id]
		if !t.valid {
			continue
		}
		path, _ := h.path(t.node, stop)
		n := path[len(path)-1]
		if current != nil && (t.node.Number <= current.Number || n.Number <= current.Number) {
			continue
		}
		if n.Number == 0 || n.Hash == stop {
			continue
		}
		if p := h.graph[n.ParentHash]; p != nil && p.node.Number != n.Number-1 {
			continue // A known invalid edge cannot be repaired by requesting it again.
		}
		if frontier == nil || n.Number > frontier.Number {
			v := n
			frontier = &v
		}
	}
	if frontier == nil {
		return nil, nil
	}
	return h.missingParent(*frontier, now)
}
func (h *HeadTracker) buildDelta(target AnchorNode) (*PathDelta, *AnchorNode, error) {
	d := &PathDelta{BaseVersion: h.state.PathVersion, NewVersion: h.state.PathVersion + 1}
	if h.state.PathVersion == 0 {
		d.Reason = InitialAnchor
		d.Appended = []DeliveredHeader{{Node: target}}
		return d, nil, nil
	}
	old, newNode := h.state.Tip, target
	if old.Hash == newNode.Hash {
		return nil, nil, nil
	}
	var oldBranch, newBranch []AnchorNode
	parent := func(n AnchorNode) (AnchorNode, *AnchorNode, error) {
		if n.Number == 0 {
			return AnchorNode{}, nil, ErrRecoveryExhausted
		}
		p := h.graph[n.ParentHash]
		if p == nil {
			return AnchorNode{}, &AnchorNode{Hash: n.ParentHash, Number: n.Number - 1}, nil
		}
		if p.node.Number != n.Number-1 {
			return AnchorNode{}, nil, ErrInvalidCandidate
		}
		return p.node, nil, nil
	}
	for old.Hash != newNode.Hash {
		if newNode.Number >= old.Number {
			newBranch = append(newBranch, newNode)
			p, missing, err := parent(newNode)
			if missing != nil || err != nil {
				return nil, missing, err
			}
			newNode = p
		} else {
			oldBranch = append(oldBranch, old)
			p, missing, err := parent(old)
			if missing != nil || err != nil {
				return nil, missing, err
			}
			old = p
		}
	}
	// A quorum retreat on the same branch is a support fact, not a retraction.
	if len(newBranch) == 0 {
		return nil, nil, nil
	}
	d.CommonAncestor = old
	d.Reason = ContinuousAdvance
	if len(oldBranch) > 0 {
		d.Reason = Reorg
	}
	for _, n := range oldBranch {
		if n.Number >= h.state.DeliveredLowerBound {
			d.Retracted = append(d.Retracted, n)
		} else {
			d.PreBirthProof = append(d.PreBirthProof, n)
		}
	}
	if old.Number < h.state.DeliveredLowerBound {
		d.Reason = PreBirthReorg
		d.PreBirthProof = append(d.PreBirthProof, old)
	}
	for i := len(newBranch) - 1; i >= 0; i-- {
		d.Appended = append(d.Appended, DeliveredHeader{Node: newBranch[i]})
	}
	return d, nil, nil
}

// Publish performs all fallible admission before the queue's atomic commit.
// Prepared material is owned by this call on every return path.
func (h *HeadTracker) Publish(q *EventQueue, p *PreparedDelta) error {
	if p == nil {
		return ErrInvalidCandidate
	}
	defer func() { p.release(); p.Event = Event{}; p.lastAudit = nil }()
	// Queue failure must not consume objects from the observation cache.
	event := p.Event
	audit := event.Audit
	event.Audit = AuditRange{}
	for i := range event.Delta.Appended {
		a := &event.Delta.Appended[i]
		a.Header = PayloadLease{}
	}
	return q.Publish(event, func(queued *Event) error {
		if h.closed {
			return ErrClosed
		}
		if h.state.PathVersion != event.Delta.BaseVersion || h.revision != p.observation {
			return ErrStalePathVersion
		}
		d := event.Delta
		for i := range d.Appended {
			a := &d.Appended[i]
			g := h.graph[a.Node.Hash]
			a.Header = g.header
			g.header = PayloadLease{}
		}
		h.state.Tip = d.Appended[len(d.Appended)-1].Node
		h.state.PathVersion = d.NewVersion
		if d.Reason == InitialAnchor {
			h.state.BirthAnchor = h.state.Tip
			h.state.DeliveredLowerBound = h.state.Tip.Number
		}
		if d.Appended[0].Node.Number < h.state.DeliveredLowerBound {
			h.state.DeliveredLowerBound = d.Appended[0].Node.Number
		}
		if p.lastAudit != nil {
			for a := h.auditFirst; a != nil && a.record.Sequence <= audit.Last; a = a.next {
				h.auditCount--
			}
			h.auditFirst = p.lastAudit.next
			p.lastAudit.next = nil
		}
		queued.Audit = audit
		if h.auditFirst == nil {
			h.auditLast = nil
		}
		h.pending = nil
		if h.requestRelease != nil {
			h.requestRelease()
			h.requestRelease = nil
		}
		h.request = nil
		h.retryAt = time.Time{}
		h.recoveryStart = time.Time{}
		h.recoveryAttempts = 0
		h.prune()
		return nil
	})
}
func (h *HeadTracker) Discard(p *PreparedDelta) {
	if p != nil {
		p.release()
		p.Event = Event{}
		p.lastAudit = nil
	}
}
func (h *HeadTracker) prune() {
	// Current slots pin only their own node. A slow source does not pin its
	// ancestry. Recovery pins the two required branches until commit/abandon.
	h.markPins()
	free := 0
	for _, e := range h.graph {
		if !e.pinned {
			e.header.Release()
			e.header = PayloadLease{}
			free++
		}
	}
	for free > h.cfg.RetainedNodes {
		var victim *graphEntry
		for _, e := range h.graph {
			if !e.pinned && (victim == nil || e.touched < victim.touched) {
				victim = e
			}
		}
		if victim == nil {
			break
		}
		h.remove(victim.node.Hash)
		free--
	}
	if h.graphSlots > 16 && len(h.graph) <= h.graphSlots/4 {
		_ = h.resizeGraph(len(h.graph))
	}
}
func (h *HeadTracker) markPins() {
	for _, e := range h.graph {
		e.pinned = false
	}
	pin := func(hash [32]byte) {
		if e := h.graph[hash]; e != nil {
			e.pinned = true
		}
	}
	path := func(n AnchorNode) {
		for {
			pin(n.Hash)
			if n.Hash == h.state.Tip.Hash || n.Number == 0 {
				break
			}
			p := h.graph[n.ParentHash]
			if p == nil || p.node.Number != n.Number-1 {
				break
			}
			n = p.node
		}
	}
	for _, t := range h.tips {
		if t.valid {
			pin(t.node.Hash)
			if h.pending != nil {
				path(t.node)
			}
		}
	}
	if h.state.PathVersion > 0 {
		pin(h.state.Tip.Hash)
	}
	if h.pending != nil {
		path(*h.pending)
		// A reorg needs the old committed interval even below the cache window.
		n := h.state.Tip
		for {
			pin(n.Hash)
			if n.Number == 0 {
				break
			}
			p := h.graph[n.ParentHash]
			if p == nil || p.node.Number != n.Number-1 {
				break
			}
			n = p.node
		}
	}
}
func (h *HeadTracker) evictIdle() {
	h.markPins()
	for hash, e := range h.graph {
		if !e.pinned {
			h.remove(hash)
		}
	}
	if h.graphSlots > 16 && len(h.graph) <= h.graphSlots/4 {
		_ = h.resizeGraph(len(h.graph))
	}
}

type TerminalError struct {
	Cause   error
	Audit   AuditRange
	release func()
}

func (e *TerminalError) Error() string { return fmt.Sprintf("subscription terminated: %v", e.Cause) }
func (e *TerminalError) Unwrap() error { return e.Cause }
func (e *TerminalError) releaseOwned() {
	e.Audit.releaseOwned()
	if e.release != nil {
		e.release()
		e.release = nil
	}
}
func (h *HeadTracker) Terminal(err error) error {
	if err == nil {
		return nil
	}
	e := &TerminalError{Cause: err, Audit: AuditRange{first: h.auditFirst}, release: h.terminalRelease}
	h.terminalRelease = nil
	if h.auditFirst != nil {
		e.Audit.First = h.auditFirst.record.Sequence
		e.Audit.Last = h.auditLast.record.Sequence
	}
	h.auditFirst = nil
	h.auditLast = nil
	h.auditCount = 0
	return e
}
func (h *HeadTracker) Close() {
	if h.closed {
		return
	}
	h.closed = true
	for hash := range h.graph {
		h.remove(hash)
	}
	h.graph = nil
	if h.graphRelease != nil {
		h.graphRelease()
		h.graphRelease = nil
	}
	h.graphSlots = 0
	for h.auditFirst != nil {
		a := h.auditFirst
		h.auditFirst = a.next
		a.next = nil
		a.release()
	}
	h.auditLast = nil
	h.auditCount = 0
	if h.requestRelease != nil {
		h.requestRelease()
		h.requestRelease = nil
	}
	h.request = nil
	h.pending = nil
	h.lastEvaluation = nil
	h.tips = nil
	h.fixedRelease()
	if h.terminalRelease != nil {
		h.terminalRelease()
		h.terminalRelease = nil
	}
}

func evidenceBytes(e Event) uint64 {
	n := uint64(cap(e.Evaluation.Witnesses)) * uint64(unsafe.Sizeof(PathWitness{}))
	for _, w := range e.Evaluation.Witnesses {
		n += uint64(cap(w.Path)) * uint64(unsafe.Sizeof(AnchorNode{}))
	}
	return n
}
