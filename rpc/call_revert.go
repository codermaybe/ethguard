package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	callDimensionExecution = "execution"
	callDimensionData      = "data"
)

// NewCallRevertPolicy resolves eth_call on two dimensions. Execution
// (a successful result versus a revert, including distinct successful
// return data) is one comparison. Revert data is abstainable: missing and
// JSON null are non-assertions, not empty bytes. A concrete data cluster
// that misses quorum, or any concrete split in unanimity mode, is deleted
// from one admitted payload and kept only as audit evidence. Generic Strict
// is a different resolver and stays single-key.
func NewCallRevertPolicy(normalize func(json.RawMessage) (string, error), opts ...StrictOption) Policy {
	cfg := strictConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	p := callRevertFunc{normalize: normalize, includeRPCError: cfg.includeRPCError, encode: cfg.encode}
	if cfg.modeSet || cfg.floorSet {
		p.fallback = &Corroboration{Mode: cfg.mode, Floor: cfg.floor}
	}
	return p
}

type callRevertFunc struct {
	normalize       func(json.RawMessage) (string, error)
	fallback        *Corroboration
	includeRPCError bool
	encode          CanonicalEncoder
}

func (p callRevertFunc) Prepare(_ context.Context, req Request) (Request, error) {
	return req, nil
}

func (p callRevertFunc) Normalize(value json.RawMessage) (string, error) {
	flat, err := p.normalize(value)
	if err != nil {
		return "", err
	}
	return callComparisonKey(flat), nil
}

func (p callRevertFunc) RequiresAgreement() bool { return true }

func (p callRevertFunc) IncludeRPCError() bool { return p.includeRPCError }

// MethodRule reports the call-revert resolver and its coarse comparison-key
// class. Revert-data states stay out of the index. This method does not
// participate in Prepare, Normalize, or Resolve.
func (p callRevertFunc) MethodRule() (MethodRule, Policy) {
	return MethodRule{Resolver: RuleResolverCallRevert, CompareKind: RuleCompareMethodSpecific, Result: resultRule(p.encode)}, nil
}

func (p callRevertFunc) CanonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	if p.encode == nil {
		return nil, ErrNotCanonicalScalar
	}
	return p.encode(raw)
}

func (p callRevertFunc) Resolve(obs []Observation) (Decision, error) {
	if p.fallback != nil {
		return p.resolve(*p.fallback, false, obs)
	}
	return p.resolve(Corroboration{}, true, obs)
}

func (p callRevertFunc) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return p.resolve(c, false, obs)
}

type callAnswer struct {
	source    SourceRef
	value     json.RawMessage
	evidence  json.RawMessage
	canonical json.RawMessage
	execution string
	data      string
	abstain   bool
	success   bool
}

func (p callRevertFunc) resolve(c Corroboration, legacy bool, obs []Observation) (Decision, error) {
	admitted, faults, err := p.admit(obs)
	if err != nil {
		return Decision{Status: Unknown, Faults: faults}, err
	}
	if len(admitted) == 0 {
		return Decision{Status: Unknown, Faults: faults}, fmt.Errorf(
			"call: no source produced a valid response (%d attempted): %w", len(obs), ErrNoValidResponse)
	}
	ranked := rankCallAnswers(admitted, func(a callAnswer) string { return a.execution })
	winner := ranked[0]
	rejected := flattenCall(ranked[1:])
	rep := representative(winner)
	decision := Decision{
		Status:         Unknown,
		Faults:         faults,
		Value:          rep.value,
		Evidence:       rep.evidence,
		Representative: rep.source,
		Support:        sourcesOf(winner),
		Reject:         sourcesOf(rejected),
	}
	if len(winner) > 0 && winner[0].success && p.encode != nil {
		canonical := make([]json.RawMessage, len(winner))
		for i := range winner {
			canonical[i] = append(json.RawMessage(nil), winner[i].canonical...)
		}
		committed, cerr := CommitCanonical(canonical)
		if cerr != nil {
			decision.Status = Unknown
			decision.Value = nil
			decision.Faults = append(decision.Faults, cerr)
			return decision, cerr
		}
		decision.Value = committed
	}
	var failed error
	switch {
	case legacy && len(ranked) != 1:
		failed = fmt.Errorf("call: %d disagreeing execution values: %w", len(ranked), ErrConflict)
	case !legacy && c.Mode == CorroborationUnanimity && len(ranked) > 1:
		failed = fmt.Errorf("call: %d disagreeing execution values: %w", len(ranked), ErrConflict)
	case !legacy && len(winner) < c.Required(len(obs)):
		required := c.Required(len(obs))
		failed = fmt.Errorf("call: required support %d, winning execution %d of %d voting members: %w",
			required, len(winner), len(obs), ErrConflict)
	case len(winner) == 1:
		decision.Status = Selected
	default:
		decision.Status = Corroborated
	}
	if failed != nil {
		decision.Dimensions = []DimensionEvidence{
			callDimension(callDimensionExecution, decision.Support, decision.Reject, nil, false),
		}
		return decision, failed
	}
	if rep.success || decision.Status == Selected {
		decision.Dimensions = callExecutionDimensions(decision, winner)
		return decision, nil
	}
	return projectRevertData(c, legacy, len(obs), decision, winner)
}

func (p callRevertFunc) admit(obs []Observation) ([]callAnswer, []error, error) {
	var faults []error
	seen := make(map[string]int, len(obs))
	var admitted []callAnswer
	for i, item := range obs {
		id := item.Source.ID
		if id == "" {
			return nil, faults, fmt.Errorf("call: observation %d has no source identity: %w", i, ErrConflict)
		}
		if first, dup := seen[id]; dup {
			return nil, faults, fmt.Errorf("call: duplicate observation for source %q (first at %d, again at %d): %w", id, first, i, ErrConflict)
		}
		seen[id] = i
		value := item.Value
		if item.Err != nil {
			if !p.includeRPCError || item.RPCError == nil {
				continue
			}
			value = item.RPCError.Raw()
		}
		var canonical json.RawMessage
		if p.encode != nil {
			encoded, err := p.encode(value)
			if err != nil && !errors.Is(err, ErrNotCanonicalScalar) {
				faults = append(faults, fmt.Errorf("encode: %w", err))
				continue
			}
			if err == nil {
				canonical = encoded
			}
		}
		flat, err := p.normalize(value)
		if err != nil {
			faults = append(faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		answer, err := classifyCall(item.Source, value, item.Evidence, flat)
		if err != nil {
			faults = append(faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		if answer.success && p.encode != nil {
			if len(canonical) == 0 {
				faults = append(faults, fmt.Errorf("encode: %w", ErrNotCanonicalScalar))
				continue
			}
			answer.canonical = append(json.RawMessage(nil), canonical...)
			answer.execution = string(answer.canonical)
		} else if answer.success {
			answer.canonical = append(json.RawMessage(nil), canonical...)
		}
		admitted = append(admitted, answer)
	}
	return admitted, faults, nil
}

func classifyCall(source SourceRef, value, evidence json.RawMessage, flat string) (callAnswer, error) {
	answer := callAnswer{
		source:   source,
		value:    append(json.RawMessage(nil), value...),
		evidence: append(json.RawMessage(nil), evidence...),
	}
	switch {
	case strings.HasPrefix(flat, "ok|"):
		answer.execution = flat
		answer.success = true
	case flat == "reverted|missing" || flat == "reverted|null":
		answer.execution = "reverted"
		answer.abstain = true
		answer.data = flat
	case strings.HasPrefix(flat, "reverted|"):
		answer.execution = "reverted"
		answer.data = strings.TrimPrefix(flat, "reverted|")
	default:
		return callAnswer{}, fmt.Errorf("call: unrecognized comparison %q", flat)
	}
	return answer, nil
}

func callComparisonKey(flat string) string {
	switch {
	case strings.HasPrefix(flat, "ok|"):
		return "execution:" + flat
	case flat == "reverted|missing":
		return "execution:reverted|data:abstain:missing"
	case flat == "reverted|null":
		return "execution:reverted|data:abstain:null"
	case strings.HasPrefix(flat, "reverted|"):
		return "execution:reverted|data:" + strings.TrimPrefix(flat, "reverted|")
	default:
		return flat
	}
}

// projectRevertData applies rule 4e to a corroborated revert. Abstainers stay
// in the voting denominator and out of data Reject. Concrete data is returned
// only when its own cluster meets the invocation quorum; otherwise one
// admitted envelope loses the data field and the bytes move to Evidence.
func projectRevertData(c Corroboration, legacy bool, voters int, decision Decision, winner []callAnswer) (Decision, error) {
	concrete, abstain := dataSplit(winner)
	decision.Status = Corroborated
	execution := callDimension(callDimensionExecution, decision.Support, decision.Reject, nil, true)
	if len(concrete) == 0 {
		rep := representative(winner)
		value, deleted := withoutData(rep)
		decision.Value = value
		decision.DeletedFields = deleted
		decision.Evidence = rep.evidence
		decision.Representative = rep.source
		decision.Dimensions = []DimensionEvidence{
			execution,
			callDimension(callDimensionData, nil, nil, sourcesOf(abstain), false),
		}
		return decision, nil
	}
	ranked := rankCallAnswers(concrete, func(a callAnswer) string { return a.data })
	dataWinner := ranked[0]
	dataRejected := flattenCall(ranked[1:])
	if revertDataReturned(c, legacy, voters, ranked) {
		rep := representative(dataWinner)
		decision.Value = rep.value
		decision.Evidence = rep.evidence
		decision.Representative = rep.source
		decision.Dimensions = []DimensionEvidence{
			execution,
			callDimension(callDimensionData, sourcesOf(dataWinner), sourcesOf(dataRejected), sourcesOf(abstain), true),
		}
		return decision, nil
	}
	rep := representative(dataWinner)
	value, deleted := withoutData(rep)
	decision.Value = value
	decision.DeletedFields = deleted
	decision.Evidence = withheldRevertData(dataWinner)
	decision.Representative = rep.source
	decision.Dimensions = []DimensionEvidence{
		execution,
		callDimension(callDimensionData, sourcesOf(dataWinner), sourcesOf(dataRejected), sourcesOf(abstain), false),
	}
	return decision, nil
}

func revertDataReturned(c Corroboration, legacy bool, voters int, ranked [][]callAnswer) bool {
	if len(ranked) == 0 {
		return false
	}
	if legacy || c.Mode == CorroborationUnanimity {
		if len(ranked) != 1 {
			return false
		}
	}
	if legacy {
		return len(ranked[0]) >= 2
	}
	return len(ranked[0]) >= c.Required(voters)
}

func callExecutionDimensions(decision Decision, winner []callAnswer) []DimensionEvidence {
	dims := []DimensionEvidence{
		callDimension(callDimensionExecution, decision.Support, decision.Reject, nil, true),
	}
	if len(winner) == 0 || winner[0].success {
		return dims
	}
	concrete, abstain := dataSplit(winner)
	returned := len(concrete) > 0
	dims = append(dims, callDimension(callDimensionData, sourcesOf(concrete), nil, sourcesOf(abstain), returned))
	return dims
}

func callDimension(name string, support, reject, abstain []SourceRef, returned bool) DimensionEvidence {
	return DimensionEvidence{Name: name, Support: support, Reject: reject, Abstain: abstain, Returned: returned}
}

func dataSplit(answers []callAnswer) (concrete, abstain []callAnswer) {
	for _, answer := range answers {
		if answer.abstain {
			abstain = append(abstain, answer)
			continue
		}
		concrete = append(concrete, answer)
	}
	return concrete, abstain
}

func rankCallAnswers(answers []callAnswer, key func(callAnswer) string) [][]callAnswer {
	groups := map[string][]callAnswer{}
	for _, answer := range answers {
		k := key(answer)
		groups[k] = append(groups[k], answer)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	ranked := make([][]callAnswer, 0, len(keys))
	for _, k := range keys {
		ranked = append(ranked, groups[k])
	}
	return ranked
}

func flattenCall(groups [][]callAnswer) []callAnswer {
	var out []callAnswer
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func sourcesOf(answers []callAnswer) []SourceRef {
	if len(answers) == 0 {
		return nil
	}
	out := make([]SourceRef, len(answers))
	for i, answer := range answers {
		out[i] = answer.source
	}
	return out
}

// representative is the first supporter of the cluster in captured order.
// The cluster slice keeps the order the answers were admitted, which is the
// captured member order, so the first element is that person. Its value and
// evidence are copies, so a later projection cannot edit the stored answer.
func representative(answers []callAnswer) callAnswer {
	candidates := make([]representativeCandidate, len(answers))
	for i, answer := range answers {
		candidates[i] = representativeCandidate{source: answer.source, value: answer.value, evidence: answer.evidence}
	}
	chosen := firstRepresentative(candidates)
	answer := answers[0]
	answer.value = chosen.value
	answer.evidence = chosen.evidence
	return answer
}

func withoutData(answer callAnswer) (json.RawMessage, []string) {
	raw, removed := deleteObjectField(answer.value, "data")
	if !removed {
		return answer.value, nil
	}
	return raw, []string{"data"}
}

func deleteObjectField(raw json.RawMessage, field string) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return append(json.RawMessage(nil), raw...), false
	}
	if _, ok := fields[field]; !ok {
		return append(json.RawMessage(nil), raw...), false
	}
	delete(fields, field)
	out, err := json.Marshal(fields)
	if err != nil {
		return append(json.RawMessage(nil), raw...), false
	}
	return out, true
}

// withheldRevertData records the data the representative withheld. Only that
// one source's data is kept; the other concrete answers stay out of the
// evidence so it cannot splice sources together.
func withheldRevertData(answers []callAnswer) json.RawMessage {
	type item struct {
		Source string          `json:"source"`
		Data   json.RawMessage `json:"data"`
	}
	body := struct {
		Withheld []item `json:"withheld_revert_data"`
	}{}
	answer := representative(answers)
	var fields map[string]json.RawMessage
	if json.Unmarshal(answer.value, &fields) == nil {
		if data, ok := fields["data"]; ok {
			body.Withheld = append(body.Withheld, item{
				Source: answer.source.ID,
				Data:   append(json.RawMessage(nil), data...),
			})
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	return raw
}
