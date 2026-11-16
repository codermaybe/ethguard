package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// CanonicalJSON reduces any JSON value to a comparable canonical form by
// decoding and re-encoding it, erasing key order and formatting differences.
func CanonicalJSON(value json.RawMessage) (string, error) {
	var decoded any
	if err := jsonsafe.Decode(value, &decoded); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	return string(canonical), err
}

// cluster is one equivalence class of admissible answers: every source whose
// response normalizes to the same key.
type cluster struct {
	key       string
	value     json.RawMessage
	evidence  json.RawMessage
	sources   []SourceRef
	canonical []json.RawMessage
}

// StrictOption tunes a strict policy at construction time.
type StrictOption func(*strictConfig)

type strictConfig struct {
	mode            CorroborationMode
	modeSet         bool
	floor           int
	floorSet        bool
	includeRPCError bool
	compareKind     string
	encode          CanonicalEncoder
	objectEncode    CanonicalEncoder
}

// WithQuorum sets the support floor H: the winning cluster must hold at
// least H sources AND a strict majority of the invocation's fixed voting
// basis, Q = max(H, floor(N/2)+1) (decisions 0028 and 0029).
//
// H is an integrity floor against collusion: size it above the number of
// sources that could be compromised together. The majority clause is
// measured against the whole voting basis, not only the sources that
// answered, so failures inside an invocation do not shrink the required
// support. A non-positive H is a configuration error. Even where the floor
// admits a single response (H = 1, one-member basis), that response stays
// Selected: a corroboration claim needs two supporters.
func WithQuorum(n int) StrictOption {
	return func(s *strictConfig) {
		if n <= 0 {
			panic("rpc: WithQuorum: n must be positive")
		}
		s.floor, s.floorSet = n, true
	}
}

// WithUnanimity selects the explicit valid-answer unanimity mode (decision
// 0029): the same support floor and majority bound as the default mode, plus
// the requirement that every admissible answer agree. It never admits a
// smaller surviving set solely because the survivors agree.
func WithUnanimity() StrictOption {
	return func(s *strictConfig) { s.mode, s.modeSet = CorroborationUnanimity, true }
}

// WithRPCErrorBodies opts a strict policy into normalizing and clustering
// structured JSON-RPC error bodies. It is intentionally opt-in: most method
// errors are not interchangeable with successful result values.
func WithRPCErrorBodies() StrictOption {
	return func(s *strictConfig) { s.includeRPCError = true }
}

// WithRuleCompareKind records the coarse comparison-key class for the
// method-rule index. It is not read by Prepare, Normalize, or Resolve.
func WithRuleCompareKind(kind string) StrictOption {
	return func(s *strictConfig) { s.compareKind = kind }
}

// WithCanonicalValue declares that the comparison key is the encoder's output,
// so one method's clusters are decided by the bytes it will return. It is not
// a registry. EncodeCheck runs before the single resolve; EncodeCommit writes
// the same bytes, which makes the within-cluster encoding guard in finish
// always true for such a method. A method that keeps a narrower key than its
// value attaches its encoder with WithCanonicalObject instead.
func WithCanonicalValue(encode CanonicalEncoder) StrictOption {
	return func(s *strictConfig) { s.encode = encode }
}

// WithCanonicalObject declares that the comparison key stays the normalizer's,
// which is what a method whose key is deliberately narrower than its value
// needs: a block key states which block was read, not the whole body. It is
// not a registry. EncodeCheck still runs first and a failure stays a Fault
// inside the denominator, but the key is not the canonical bytes, so the
// within-cluster encoding guard in finish still carries weight. It has no
// production caller today, and block re-encoding is what it is reserved for.
func WithCanonicalObject(encode CanonicalEncoder) StrictOption {
	return func(s *strictConfig) { s.objectEncode = encode }
}

// Strict binds a response normalizer to strict-corroboration resolution. Method
// authors supply only the normalizer — the one part that varies per method —
// instead of re-implementing the Policy shell.
//
// Corroboration options install the decision-0028 adjudication (majority or
// unanimity over the invocation's fixed voting basis). Without one, the
// legacy decision-0006 default applies: every valid response must agree, and
// a single uncorroborated response is Selected rather than Corroborated. The
// legacy default is retained for direct construction — the reference
// establishment paths depend on it by recorded decision
// (docs/design/method-contracts.md) — while the client's default path
// always resolves under a captured corroboration snapshot through
// ResolveCorroborated.
func Strict(normalize func(json.RawMessage) (string, error), opts ...StrictOption) Policy {
	cfg := strictConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	p := strictFunc{normalize: normalize, includeRPCError: cfg.includeRPCError, compareKind: cfg.compareKind, encode: cfg.encode, objectEncode: cfg.objectEncode}
	if cfg.modeSet || cfg.floorSet {
		p.corroboration = &Corroboration{Mode: cfg.mode, Floor: cfg.floor}
	}
	return p
}

type strictFunc struct {
	normalize       func(json.RawMessage) (string, error)
	corroboration   *Corroboration
	includeRPCError bool
	compareKind     string
	encode          CanonicalEncoder
	objectEncode    CanonicalEncoder
}

func (f strictFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (f strictFunc) Normalize(value json.RawMessage) (string, error) {
	return f.normalize(value)
}

func (f strictFunc) RequiresAgreement() bool { return true }

func (f strictFunc) IncludeRPCError() bool { return f.includeRPCError }

// MethodRule reports strict resolution and the coarse comparison-key class
// supplied at construction. It does not participate in request handling.
// KeySource reads the same declaration cluster() reads: WithCanonicalValue
// means the key is this encoder's output, WithCanonicalObject means the key
// stays the normalizer's. Strict always declares the column, because a Strict
// method's key is one of those two things: the three cases are "the key is the
// encoder's output", "the key is the normalizer's although an encoder is
// attached", and "the key is the normalizer's and no encoder is attached yet".
// Only this resolver declares it, so a median, local, or call-revert method
// leaves it empty and renders as none.
func (f strictFunc) MethodRule() (MethodRule, Policy) {
	return MethodRule{Resolver: RuleResolverStrict, CompareKind: f.compareKind, KeySource: f.keySource(), Result: resultRule(f.canonicalEncoder())}, nil
}

func (f strictFunc) keySource() string {
	if f.encode != nil {
		return RuleKeyCanonicalBytes
	}
	return RuleKeyNormalizer
}

func (f strictFunc) canonicalEncoder() CanonicalEncoder {
	if f.encode != nil {
		return f.encode
	}
	return f.objectEncode
}

func (f strictFunc) CanonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	encode := f.canonicalEncoder()
	if encode == nil {
		return nil, ErrNotCanonicalScalar
	}
	return encode(raw)
}

// skippedAnswer records one observation that could not enter a cluster. A
// "response" cause is classification-only: the raw transport error stays out
// of the resolver's fault list because the coordinator attributes those, and
// it always did. "encode" and "normalize" causes are fault-worthy: they were
// resolver faults before the outcome statement existed and keep that shape.
type skippedAnswer struct {
	source SourceRef
	cause  string
	err    error
}

// skippedFaults renders the fault-worthy skipped answers as the resolver's
// fault list, keeping the attribution shape the resolve paths have always
// produced. Response-cause skips are deliberately absent: the coordinator's
// per-source fault seam owns transport errors.
func skippedFaults(skipped []skippedAnswer) []error {
	var faults []error
	for _, item := range skipped {
		if item.cause == "response" {
			continue
		}
		faults = append(faults, sourceFault(item.source, item.cause, item.err))
	}
	return faults
}

// verdictsFromSources is the shared per-source statement builder: a source in
// the winning set supported, a source in the dissenting set dissented, and
// every other observed source unusable, carrying its own skip error when one
// was recorded. The three sets are exactly the fact each resolver's own
// resolution computes — strict from its clusters, call-revert from its
// execution groups — so both statements read from the same expression of
// "who lost what". The source list fixes the statement's order and slots,
// including sources whose answer was skipped.
func verdictsFromSources(sources []SourceRef, supported, dissented map[string]struct{}, skipped []skippedAnswer) []SourceVerdict {
	unusable := make(map[string]skippedAnswer, len(skipped))
	for _, item := range skipped {
		unusable[item.source.ID] = item
	}
	verdicts := make([]SourceVerdict, 0, len(sources))
	for _, source := range sources {
		switch {
		case containsSourceID(supported, source.ID):
			verdicts = append(verdicts, SourceVerdict{Source: source, Kind: VerdictSupported})
		case containsSourceID(dissented, source.ID):
			verdicts = append(verdicts, SourceVerdict{Source: source, Kind: VerdictDissented})
		default:
			skip := unusable[source.ID]
			verdicts = append(verdicts, SourceVerdict{Source: source, Kind: VerdictUnusable, Err: skip.err})
		}
	}
	sortSourceVerdicts(verdicts)
	return verdicts
}

// verdictsFromRanked is the cluster-set entry of the statement builder: the
// winning cluster's members supported, the losing clusters' dissented. An
// empty cluster set — the shape a failed clustering reports — classifies
// every source as unusable instead of panicking on the absent winner.
func verdictsFromRanked(sources []SourceRef, ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
	supported := make(map[string]struct{})
	dissented := make(map[string]struct{})
	if len(ranked) > 0 {
		for _, source := range ranked[0].sources {
			supported[source.ID] = struct{}{}
		}
		for _, cl := range ranked[1:] {
			for _, source := range cl.sources {
				dissented[source.ID] = struct{}{}
			}
		}
	}
	return verdictsFromSources(sources, supported, dissented, skipped)
}

// verdictsFromClassified is the classified-set entry of the statement
// builder, for resolvers that compute their own support and dissent sets.
func verdictsFromClassified(obs []Observation, supported, dissented map[string]struct{}, skipped []skippedAnswer) []SourceVerdict {
	return verdictsFromSources(sourcesOfObservations(obs), supported, dissented, skipped)
}

// verdictsFromCanonical is the canonical-route entry of the shared statement
// builder: the slots come from the canonical observations, failed ones
// included, so no source loses its statement.
func verdictsFromCanonical(cobs []CanonicalObservation, ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
	return verdictsFromRanked(sourcesOfCanonical(cobs), ranked, skipped)
}

// verdictsFromClusters classifies every observation once clustering and
// adjudication are done: sources in the winning cluster supported, sources in
// losing clusters dissented, and every remaining attempted source was
// unusable. A source whose request never started is indistinguishable to the
// resolver — it sees only observations — so the coordinator reclassifies it
// as absent from its own attempt bookkeeping.
func verdictsFromClusters(obs []Observation, ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
	return verdictsFromRanked(sourcesOfObservations(obs), ranked, skipped)
}

func containsSourceID(set map[string]struct{}, id string) bool {
	_, ok := set[id]
	return ok
}

func (f strictFunc) Resolve(obs []Observation) (Decision, error) {
	if f.corroboration == nil {
		return f.resolveLegacy(obs)
	}
	return f.resolve(*f.corroboration, obs)
}

// ResolveCorroborated resolves under the invocation's frozen corroboration
// snapshot, ignoring construction-time corroboration options. The aggregator
// calls this with the mode and floor captured before any request was issued
// (decision 0025).
func (f strictFunc) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	decision, _, err := f.adjudicated(c, obs)
	return decision, err
}

// ResolveOutcome is Resolve with the per-source statement attached: the same
// decision, plus every source's verdict. Consumers project faults and
// observability events from the outcome instead of re-running value
// classification.
func (f strictFunc) ResolveOutcome(obs []Observation) (Outcome, error) {
	if f.corroboration == nil {
		return f.outcomeLegacy(obs)
	}
	return f.outcome(*f.corroboration, obs)
}

// ResolveOutcomeCorroborated mirrors ResolveCorroborated with the per-source
// statement attached.
func (f strictFunc) ResolveOutcomeCorroborated(c Corroboration, obs []Observation) (Outcome, error) {
	return f.outcome(c, obs)
}

// ResolveCanonical is the canonical route of the same resolution: processed
// observations in, the same clustering, adjudication, verdicts, and commit
// kernel out. The raw normalizer and encoder are never consulted here — the
// canonical bytes are the comparison key and the commit material. The
// construction-time corroboration configuration decides the mode exactly as
// ResolveOutcome does; there is no zero-snapshot shortcut for the legacy
// route.
func (f strictFunc) ResolveCanonical(cobs []CanonicalObservation) (Outcome, error) {
	if f.corroboration == nil {
		return f.canonicalLegacy(cobs)
	}
	return f.canonicalCore(*f.corroboration, cobs)
}

// ResolveCanonicalCorroborated is the canonical route under the invocation's
// frozen corroboration snapshot, mirroring ResolveOutcomeCorroborated.
func (f strictFunc) ResolveCanonicalCorroborated(c Corroboration, cobs []CanonicalObservation) (Outcome, error) {
	return f.canonicalCore(c, cobs)
}

// resolve clusters admissible answers and adjudicates them against the
// invocation's voting basis.
func (f strictFunc) resolve(c Corroboration, obs []Observation) (Decision, error) {
	decision, _, err := f.adjudicated(c, obs)
	return decision, err
}

// adjudicated is the shared core of resolve and outcome: cluster, adjudicate
// against the fixed basis, commit canonical bytes, and classify every source.
func (f strictFunc) adjudicated(c Corroboration, obs []Observation) (Decision, []SourceVerdict, error) {
	ranked, skipped, err := f.cluster(obs)
	if err != nil {
		return Decision{Status: Unknown, Faults: skippedFaults(skipped)}, nil, err
	}
	decision, err := c.adjudicate(len(obs), ranked, skippedFaults(skipped))
	decision, err = f.finish(decision, err, ranked)
	return decision, verdictsFromClusters(obs, ranked, skipped), err
}

func (f strictFunc) outcome(c Corroboration, obs []Observation) (Outcome, error) {
	decision, verdicts, err := f.adjudicated(c, obs)
	return Outcome{Decision: decision, Verdicts: verdicts}, err
}

// resolveLegacy is the decision-0006 default for policies constructed
// without corroboration options: agreement among every valid response, with
// one uncorroborated response downgraded to Selected.
func (f strictFunc) resolveLegacy(obs []Observation) (Decision, error) {
	decision, _, err := f.legacyCore(obs)
	return decision, err
}

func (f strictFunc) outcomeLegacy(obs []Observation) (Outcome, error) {
	decision, verdicts, err := f.legacyCore(obs)
	return Outcome{Decision: decision, Verdicts: verdicts}, err
}

func (f strictFunc) legacyCore(obs []Observation) (Decision, []SourceVerdict, error) {
	ranked, skipped, err := f.cluster(obs)
	if err != nil {
		return Decision{Status: Unknown, Faults: skippedFaults(skipped)}, nil, err
	}
	return legacyCoreClusters(ranked, skipped, sourcesOfObservations(obs), f.finish)
}

// canonicalCore is the corroborated canonical route: the same adjudication
// and commit kernel as the raw route, over processed results.
func (f strictFunc) canonicalCore(c Corroboration, cobs []CanonicalObservation) (Outcome, error) {
	ranked, skipped, err := clusterCanonical(cobs)
	if err != nil {
		decision := Decision{Status: Unknown, Faults: skippedFaults(skipped)}
		return Outcome{Decision: decision, Verdicts: verdictsFromCanonical(cobs, nil, skipped)}, err
	}
	decision, err := c.adjudicate(len(cobs), ranked, skippedFaults(skipped))
	decision, err = commitProcessed(decision, err, ranked)
	return Outcome{Decision: decision, Verdicts: verdictsFromCanonical(cobs, ranked, skipped)}, err
}

// canonicalLegacy is the no-snapshot canonical route: the decision-0006
// default over processed results, sharing the legacy cluster core with the
// raw route and committing through the unconditional kernel.
func (f strictFunc) canonicalLegacy(cobs []CanonicalObservation) (Outcome, error) {
	ranked, skipped, err := clusterCanonical(cobs)
	if err != nil {
		decision := Decision{Status: Unknown, Faults: skippedFaults(skipped)}
		return Outcome{Decision: decision, Verdicts: verdictsFromCanonical(cobs, nil, skipped)}, err
	}
	decision, verdicts, err := legacyCoreClusters(ranked, skipped, sourcesOfCanonical(cobs), commitProcessed)
	return Outcome{Decision: decision, Verdicts: verdicts}, err
}

func sourcesOfObservations(obs []Observation) []SourceRef {
	sources := make([]SourceRef, len(obs))
	for i, item := range obs {
		sources[i] = item.Source
	}
	return sources
}

func sourcesOfCanonical(cobs []CanonicalObservation) []SourceRef {
	sources := make([]SourceRef, len(cobs))
	for i, item := range cobs {
		sources[i] = item.observation.Source
	}
	return sources
}

// legacyCoreClusters is the shared decision-0006 core over already-clustered
// answers: the winner's value, provenance, and representative; agreement
// among every valid response with a lone response downgraded to Selected;
// then the route's commit (the raw route gates on its encoder declaration,
// the canonical route commits unconditionally) and the shared per-source
// statement.
func legacyCoreClusters(ranked []cluster, skipped []skippedAnswer, sources []SourceRef, commit func(Decision, error, []cluster) (Decision, error)) (Decision, []SourceVerdict, error) {
	var decision Decision
	decision.Status = Unknown
	decision.Faults = skippedFaults(skipped)
	winner := ranked[0]
	decision.Value = winner.value
	decision.Provenance = append(json.RawMessage(nil), winner.evidence...)
	decision.Representative = winner.sources[0]
	decision.Support = winner.sources
	for _, cl := range ranked[1:] {
		decision.Reject = append(decision.Reject, cl.sources...)
	}
	var ret error
	switch {
	case len(ranked) != 1:
		ret = fmt.Errorf("strict: %d disagreeing values: %w", len(ranked), ErrConflict)
	case len(winner.sources) == 1:
		decision.Status = Selected // no corroboration, no Corroborated claim
	default:
		decision.Status = Corroborated
	}
	decision, ret = commit(decision, ret, ranked)
	supported := make(map[string]struct{})
	for _, source := range winner.sources {
		supported[source.ID] = struct{}{}
	}
	dissented := make(map[string]struct{})
	for _, cl := range ranked[1:] {
		for _, source := range cl.sources {
			dissented[source.ID] = struct{}{}
		}
	}
	return decision, verdictsFromSources(sources, supported, dissented, skipped), ret
}

// finish commits canonical bytes after the single resolve. EncodeCheck
// failures already stayed out of the cluster and inside the denominator. A
// within-cluster disagreement does not select another original and does not
// vote again. Rejected-cluster disagreements are diagnostics only and do not
// change the adjudication, membership, or denominator (design section 14.1).
//
// The gate below is the raw route's declaration that its comparison key is
// committed canonical bytes; the commit itself is the shared kernel
// commitProcessed, which the canonical route runs unconditionally — its
// cluster material is already canonical, so committing never depends on the
// raw encoder callbacks being attached.
func (f strictFunc) finish(decision Decision, err error, ranked []cluster) (Decision, error) {
	if f.canonicalEncoder() == nil {
		return decision, err
	}
	return commitProcessed(decision, err, ranked)
}

// commitProcessed is the shared canonical commit kernel: rejected-cluster
// encoding diagnostics, then the winning cluster's committed bytes onto
// Value. Both routes call this one implementation; the routes differ only in
// where their cluster material came from. The within-cluster guard below is
// always true for a method whose key is its canonical bytes, because
// clustering on the key puts byte-equal values in one cluster; it fires only
// for a method that keeps a narrower key than its value, such as a block
// method wired with WithCanonicalObject. If it ever fires for a
// WithCanonicalValue method, some path computed that method's key from
// something other than its encoder, and the clusters were not decided by the
// bytes being committed.
func commitProcessed(decision Decision, err error, ranked []cluster) (Decision, error) {
	for _, rejected := range ranked[1:] {
		if cerr := checkCanonical(rejected.canonical); cerr != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("rejected cluster: %w", encodingDiagnostic(rejected, cerr)))
		}
	}
	winner := ranked[0]
	committed, cerr := CommitCanonical(winner.canonical)
	if cerr != nil {
		decision.Status = Unknown
		decision.Value = nil
		decision.Faults = append(decision.Faults, encodingDiagnostic(winner, cerr))
		return decision, cerr
	}
	decision.Value = committed
	return decision, err
}

// encodingDiagnostic runs only for an inconsistent cluster. It summarizes
// the already encoded bytes without copying payloads or interpreting sources.
func encodingDiagnostic(cl cluster, err error) error {
	parts := make([]string, len(cl.sources))
	for i, source := range cl.sources {
		encoded := cl.canonical[i]
		digest := sha256.Sum256(encoded)
		parts[i] = fmt.Sprintf("source %q canonical length %d sha256 %x", source.ID, len(encoded), digest)
	}
	return fmt.Errorf("%w; encodings: %s", err, strings.Join(parts, "; "))
}

// cluster groups admissible answers and ranks the clusters by size, breaking
// ties by key, so identical inputs always yield identical Support and Reject
// regardless of arrival order or map iteration. The key source is the
// declaration made at assembly, not an inference: a method that declared
// WithCanonicalValue clusters on the canonical JSON EncodeCheck produced, and
// EncodeCommit writes those same bytes. A method that declared
// WithCanonicalObject still runs EncodeCheck first and keeps a failure as a
// Fault inside the denominator, but its key comes from the normalizer, and
// EncodeCommit writes the value's own canonical bytes. Unmigrated methods keep
// the normalizer key, including block number|hash. The cluster's Value and
// Provenance both come from the first supporter in the captured member order,
// which is the order of obs; when that supporter has no evidence the
// cluster's evidence stays empty instead of borrowing a later supporter's.
func (f strictFunc) cluster(obs []Observation) ([]cluster, []skippedAnswer, error) {
	var skipped []skippedAnswer
	groups := make(map[string][]representativeCandidate)
	seen := make(map[string]int, len(obs))
	for i, item := range obs {
		id := item.Source.ID
		if id == "" {
			return nil, skipped, fmt.Errorf("strict: observation %d has no source identity: %w", i, ErrConflict)
		}
		if first, dup := seen[id]; dup {
			return nil, skipped, fmt.Errorf(
				"strict: duplicate observation for source %q (first at %d, again at %d): %w",
				id, first, i, ErrConflict)
		}
		seen[id] = i
		value := item.Value
		if item.Err != nil {
			if !f.includeRPCError || item.RPCError == nil {
				skipped = append(skipped, skippedAnswer{source: item.Source, cause: "response", err: item.Err})
				continue
			}
			value = item.RPCError.Raw()
		}
		var canonical json.RawMessage
		if encode := f.canonicalEncoder(); encode != nil {
			encoded, err := encode(value)
			if err != nil {
				skipped = append(skipped, skippedAnswer{source: item.Source, cause: "encode", err: err})
				continue
			}
			canonical = append(json.RawMessage(nil), encoded...)
		}
		var key string
		if f.encode != nil {
			key = string(canonical)
		} else {
			var err error
			key, err = f.normalize(value)
			if err != nil {
				skipped = append(skipped, skippedAnswer{source: item.Source, cause: "normalize", err: err})
				continue
			}
		}
		groups[key] = append(groups[key], representativeCandidate{source: item.Source, value: value, evidence: item.Evidence, canonical: canonical})
	}
	return rankClusters(groups, skipped, len(obs))
}

// clusterCanonical groups processed results by their canonical bytes — the
// key by construction, so the route never calls the raw normalizer or
// encoder, and the cluster's commit material is the result's own value. A
// failed observation keeps its slot as a response-cause skip: the canonical
// route processes only available sources, and structured provider error
// bodies stay the raw routes' domain (no canonical method opts into them).
// Identity rules mirror the raw route exactly: an empty or duplicated source
// identity is the process-conflict sentinel, not a skipped answer.
func clusterCanonical(cobs []CanonicalObservation) ([]cluster, []skippedAnswer, error) {
	var skipped []skippedAnswer
	groups := make(map[string][]representativeCandidate)
	seen := make(map[string]int, len(cobs))
	for i, item := range cobs {
		id := item.observation.Source.ID
		if id == "" {
			return nil, skipped, fmt.Errorf("strict: canonical observation %d has no source identity: %w", i, ErrConflict)
		}
		if first, dup := seen[id]; dup {
			return nil, skipped, fmt.Errorf(
				"strict: duplicate canonical observation for source %q (first at %d, again at %d): %w",
				id, first, i, ErrConflict)
		}
		seen[id] = i
		if item.observation.Err != nil {
			skipped = append(skipped, skippedAnswer{source: item.observation.Source, cause: "response", err: item.observation.Err})
			continue
		}
		value := item.result.Value()
		groups[string(value)] = append(groups[string(value)], representativeCandidate{
			source:    item.observation.Source,
			value:     value,
			evidence:  item.result.Evidence(),
			canonical: append(json.RawMessage(nil), value...),
		})
	}
	return rankClusters(groups, skipped, len(cobs))
}

// rankClusters is the shared cluster assembly: size-ranked clusters with
// key-ordered ties, each carrying its sources and its members' canonical
// bytes in captured order. An empty group set is the no-valid-response
// sentinel with the attempted count kept visible.
func rankClusters(groups map[string][]representativeCandidate, skipped []skippedAnswer, attempted int) ([]cluster, []skippedAnswer, error) {
	if len(groups) == 0 {
		return nil, skipped, fmt.Errorf(
			"strict: no source produced a valid response (%d attempted): %w", attempted, ErrNoValidResponse)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	ranked := make([]cluster, 0, len(keys))
	for _, key := range keys {
		members := groups[key]
		rep := firstRepresentative(members)
		sources := make([]SourceRef, len(members))
		canonical := make([]json.RawMessage, len(members))
		for i, member := range members {
			sources[i] = member.source
			canonical[i] = append(json.RawMessage(nil), member.canonical...)
		}
		ranked = append(ranked, cluster{key: key, value: rep.value, evidence: rep.evidence, sources: sources, canonical: canonical})
	}
	return ranked, skipped, nil
}
