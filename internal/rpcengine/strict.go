package rpcengine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// cluster is one equivalence class of admissible answers: every source whose
// response normalizes to the same key.
type cluster struct {
	key       string
	value     json.RawMessage
	evidence  json.RawMessage
	sources   []rpc.SourceRef
	canonical []json.RawMessage
}

// StrictOption tunes a strict policy at construction time.
type StrictOption func(*strictConfig)

type strictConfig struct {
	mode            rpc.CorroborationMode
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
	return func(s *strictConfig) { s.mode, s.modeSet = rpc.CorroborationUnanimity, true }
}

// WithRPCErrorBodies opts a strict policy into normalizing and clustering
// structured JSON-RPC error bodies. It is intentionally opt-in: most method
// errors are not interchangeable with successful result values.
func WithRPCErrorBodies() StrictOption {
	return func(s *strictConfig) { s.includeRPCError = true }
}

// WithRuleCompareKind records the coarse comparison-key class for the
// method-rule index. It is not read by Normalize or Resolve.
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
// instead of re-implementing the Resolver contract.
//
// Corroboration options install the decision-0028 adjudication (majority or
// unanimity over the invocation's fixed voting basis). Without one, the
// decision-0006 valid-answer agreement applies: all valid responses must
// agree, and a single response is Selected rather than Corroborated. The
// valid-answer rule also serves direct construction: the reference
// establishment paths depend on it by recorded decision
// (docs/design/method-contracts.md) — while the client's default path
// always resolves under a captured corroboration snapshot through
// ResolveCorroborated.
func Strict(normalize func(json.RawMessage) (string, error), opts ...StrictOption) Resolver {
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
func (f strictFunc) MethodRule() MethodRule {
	return MethodRule{Resolver: RuleResolverStrict, CompareKind: f.compareKind, KeySource: f.keySource(), Result: resultRule(f.canonicalEncoder())}
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
	return encodeCanonicalValue(f.canonicalEncoder(), raw)
}

// skippedAnswer records one observation that could not enter a cluster. A
// "response" cause is classification-only: the raw transport error stays out
// of the resolver's fault list because the coordinator attributes those, and
// it always did. "encode" and "normalize" causes are fault-worthy: they were
// resolver faults before the outcome statement existed and keep that shape.
type skippedAnswer struct {
	source rpc.SourceRef
	cause  string
	err    error
}

// verdictsFromSources uses only facts produced by the actual interpretation
// loop. A source not classified before an internal abort remains unassessed.
func verdictsFromSources(obs []rpc.Observation, supported, dissented map[string]struct{}, skipped []skippedAnswer) []SourceVerdict {
	unusable := make(map[string]skippedAnswer, len(skipped))
	for _, item := range skipped {
		unusable[item.source.ID] = item
	}
	verdicts := initialVerdicts(obs)
	for i := range verdicts {
		v := &verdicts[i]
		switch {
		case containsSourceID(supported, v.Source.ID):
			v.Kind, v.Method = rpc.VerdictSupported, rpc.Interpretation{State: rpc.InterpretationValidated}
		case containsSourceID(dissented, v.Source.ID):
			v.Kind, v.Method = rpc.VerdictDissented, rpc.Interpretation{State: rpc.InterpretationValidated}
		default:
			if skip, exists := unusable[v.Source.ID]; exists {
				if skip.cause != "response" {
					rejectVerdict(v, fmt.Errorf("%s: %w", skip.cause, skip.err))
				} else if obs[i].Response.CandidateErr(true) == nil {
					v.Kind, v.Method = rpc.VerdictUnusable, rpc.Interpretation{State: rpc.InterpretationExcluded, Cause: skip.err}
				} else {
					unavailableVerdict(v, obs[i].Response)
				}
			}
		}
	}
	sortSourceVerdicts(verdicts)
	return verdicts
}

// verdictsFromRanked is the cluster-set entry of the statement builder: the
// winning cluster's members supported, the losing clusters' dissented. An
// empty cluster set — the shape a failed clustering reports — classifies
// every source as unusable instead of panicking on the absent winner.
func verdictsFromRanked(obs []rpc.Observation, ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
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
	return verdictsFromSources(obs, supported, dissented, skipped)
}

// verdictsFromClassified is the classified-set entry of the statement
// builder, for resolvers that compute their own support and dissent sets.
func verdictsFromClassified(obs []rpc.Observation, supported, dissented map[string]struct{}, skipped []skippedAnswer) []SourceVerdict {
	return verdictsFromSources(obs, supported, dissented, skipped)
}

// verdictsFromCanonical is the canonical-route entry of the shared statement
// builder: the slots come from the canonical observations, failed ones
// included, so no source loses its statement.
func verdictsFromCanonical(cobs []CanonicalObservation, ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
	return verdictsFromRanked(observationsOfCanonical(cobs), ranked, skipped)
}

// verdictsFromClusters classifies every observation once clustering and
// adjudication are done: sources in the winning cluster supported, sources in
// losing clusters dissented, and every remaining attempted source was
// unusable. A source whose request never started is indistinguishable to the
// resolver — it sees only observations — so the coordinator reclassifies it
// as absent from its own attempt bookkeeping.
func verdictsFromClusters(obs []rpc.Observation, ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
	return verdictsFromRanked(obs, ranked, skipped)
}

func containsSourceID(set map[string]struct{}, id string) bool {
	_, ok := set[id]
	return ok
}

func (f strictFunc) Resolve(obs []rpc.Observation) (Outcome, error) {
	if f.corroboration == nil {
		return f.outcomeValidAnswers(obs)
	}
	return f.outcome(*f.corroboration, obs)
}

func (f strictFunc) ResolveCorroborated(c Corroboration, obs []rpc.Observation) (Outcome, error) {
	return f.outcome(c, obs)
}

// adjudicationCore is the shared corroborated kernel of the raw and canonical
// routes: adjudicate the ranked clusters against the fixed basis, apply the
// route's commit, and state every source. A failed clustering keeps the
// Unknown decision and the judgments already formed. Unprocessed candidates
// remain unassessed; no downstream consumer reruns method checks.
func adjudicationCore(
	c Corroboration,
	attempted int,
	ranked []cluster,
	skipped []skippedAnswer,
	clusterErr error,
	commit func(rpc.Decision, error, []cluster) (rpc.Decision, error),
	verdicts func([]cluster, []skippedAnswer) []SourceVerdict,
	failedVerdicts func([]skippedAnswer) []SourceVerdict,
) (rpc.Decision, []SourceVerdict, error) {
	if clusterErr != nil {
		var stated []SourceVerdict
		if failedVerdicts != nil {
			stated = failedVerdicts(skipped)
		}
		return rpc.Decision{Status: rpc.Unknown, Adjudication: rankedAdjudication(attempted, c.Required(attempted), nil), Faults: nil}, stated, clusterErr
	}
	decision, err := c.adjudicate(attempted, ranked, nil)
	decision, err = commit(decision, err, ranked)
	return decision, verdicts(ranked, skipped), err
}

// adjudicated is the shared core of resolve and outcome: cluster, adjudicate
// against the fixed basis, commit canonical bytes, and classify every source.
func (f strictFunc) adjudicated(c Corroboration, obs []rpc.Observation) (rpc.Decision, []SourceVerdict, error) {
	ranked, skipped, err := f.cluster(obs)
	return adjudicationCore(c, len(obs), ranked, skipped, err, f.finish,
		func(ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
			return verdictsFromClusters(obs, ranked, skipped)
		},
		func(skipped []skippedAnswer) []SourceVerdict { return verdictsFromClusters(obs, nil, skipped) })
}

func (f strictFunc) outcome(c Corroboration, obs []rpc.Observation) (Outcome, error) {
	decision, verdicts, err := f.adjudicated(c, obs)
	return completeOutcome(obs, decision, verdicts, err)
}

func (f strictFunc) outcomeValidAnswers(obs []rpc.Observation) (Outcome, error) {
	decision, verdicts, err := f.validAnswersCore(obs)
	return completeOutcome(obs, decision, verdicts, err)
}

func (f strictFunc) validAnswersCore(obs []rpc.Observation) (rpc.Decision, []SourceVerdict, error) {
	ranked, skipped, err := f.cluster(obs)
	if err != nil {
		return rpc.Decision{Status: rpc.Unknown, Adjudication: rankedAdjudication(len(obs), 1, nil), Faults: nil}, verdictsFromClusters(obs, nil, skipped), err
	}
	return adjudicateValidAnswers(ranked, skipped, obs, f.finish)
}

// canonicalCore is the corroborated canonical route: the same adjudication
// and commit kernel as the raw route, over processed results.
func (f canonicalStrict) canonicalCore(c Corroboration, cobs []CanonicalObservation) (Outcome, error) {
	ranked, skipped, err := clusterCanonical(cobs)
	decision, verdicts, err := adjudicationCore(c, len(cobs), ranked, skipped, err, commitProcessed,
		func(ranked []cluster, skipped []skippedAnswer) []SourceVerdict {
			return verdictsFromCanonical(cobs, ranked, skipped)
		},
		func(skipped []skippedAnswer) []SourceVerdict {
			return verdictsFromCanonical(cobs, nil, skipped)
		})
	return completeOutcome(observationsOfCanonical(cobs), decision, verdicts, err)
}

func observationsOfCanonical(cobs []CanonicalObservation) []rpc.Observation {
	obs := make([]rpc.Observation, len(cobs))
	for i, item := range cobs {
		obs[i] = item.observation
	}
	return obs
}

// adjudicateValidAnswers is the direct raw Strict contract from decision 0006:
// every valid response must agree and a lone response stays Selected. Its
// encoder declaration controls commit; source judgments use the shared kernel.
func adjudicateValidAnswers(ranked []cluster, skipped []skippedAnswer, obs []rpc.Observation, commit func(rpc.Decision, error, []cluster) (rpc.Decision, error)) (rpc.Decision, []SourceVerdict, error) {
	winner := ranked[0]
	decision := winnerDecision(ranked, nil)
	decision.Adjudication = rankedAdjudication(len(obs), 1, ranked)
	decision.Adjudication.Reason = rpc.ReasonAccepted
	var ret error
	switch {
	case len(ranked) != 1:
		decision.Adjudication.Reason = rpc.ReasonConflictingCandidates
		ret = fmt.Errorf("strict: %d disagreeing values: %w", len(ranked), rpc.ErrConflict)
	case len(winner.sources) == 1:
		decision.Status = rpc.Selected // no corroboration, no Corroborated claim
	default:
		decision.Status = rpc.Corroborated
	}
	decision, ret = commit(decision, ret, ranked)
	return decision, verdictsFromRanked(obs, ranked, skipped), ret
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
func (f strictFunc) finish(decision rpc.Decision, err error, ranked []cluster) (rpc.Decision, error) {
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
func commitProcessed(decision rpc.Decision, err error, ranked []cluster) (rpc.Decision, error) {
	for _, rejected := range ranked[1:] {
		if cerr := checkCanonical(rejected.canonical); cerr != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("rejected cluster: %w", encodingDiagnostic(rejected, cerr)))
		}
	}
	winner := ranked[0]
	committed, cerr := CommitCanonical(winner.canonical)
	if cerr != nil {
		decision.Status = rpc.Unknown
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
// EncodeCommit writes the value's own canonical bytes. A raw resolver without
// an encoder declaration keeps its normalizer key and original representative
// bytes. The cluster's Value and
// Provenance both come from the first supporter in the captured member order,
// which is the order of obs; when that supporter has no evidence the
// cluster's evidence stays empty instead of borrowing a later supporter's.
func (f strictFunc) cluster(obs []rpc.Observation) ([]cluster, []skippedAnswer, error) {
	var skipped []skippedAnswer
	groups := make(map[string][]representativeCandidate)
	seen := make(map[string]int, len(obs))
	for i, item := range obs {
		id := item.Source.ID
		if id == "" {
			return nil, skipped, missingIdentity("strict", "observation", i)
		}
		if first, dup := seen[id]; dup {
			return nil, skipped, duplicateIdentity("strict", "observation", id, first, i)
		}
		seen[id] = i
		value, candidateErr := item.Response.Candidate(f.includeRPCError)
		if candidateErr != nil {
			skipped = append(skipped, skippedAnswer{source: item.Source, cause: "response", err: candidateErr})
			continue
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
			return nil, skipped, missingIdentity("strict", "canonical observation", i)
		}
		if first, dup := seen[id]; dup {
			return nil, skipped, duplicateIdentity("strict", "canonical observation", id, first, i)
		}
		seen[id] = i
		if item.observation.Response.Err() != nil {
			skipped = append(skipped, skippedAnswer{source: item.observation.Source, cause: "response", err: item.observation.Response.Err()})
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
		return nil, skipped, noValidResponse("strict", attempted)
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
		sources := make([]rpc.SourceRef, len(members))
		canonical := make([]json.RawMessage, len(members))
		for i, member := range members {
			sources[i] = member.source
			canonical[i] = append(json.RawMessage(nil), member.canonical...)
		}
		ranked = append(ranked, cluster{key: key, value: rep.value, evidence: rep.evidence, sources: sources, canonical: canonical})
	}
	return ranked, skipped, nil
}
