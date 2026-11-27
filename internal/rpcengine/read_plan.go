package rpcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// ReferenceShape names the request shape that can carry a moving tag.
type ReferenceShape string

const (
	ReferenceShapeNone           ReferenceShape = ""
	ReferenceShapeHashAt         ReferenceShape = "hash-at"
	ReferenceShapeHeightAt       ReferenceShape = "height-at"
	ReferenceShapeHeightFieldsAt ReferenceShape = "height-fields-at"
)

// ReferenceSpec declares where a request can ride a moving tag. Declaring is
// all a method does: the aggregator establishes the reference through the
// reference stage on the invocation's frozen basis, and PrepareWithReference
// rewrites from the established evidence. A zero spec means the method never
// needs a reference. See docs/design/codebase-topology.md.
type ReferenceSpec struct {
	Kind      ReferenceShape
	Positions []int
	ParamPos  int
	Fields    []string
	// EIP1898 marks the object form {blockNumber: tag} accepted at the
	// single anchored position, rewritten to {blockHash}.
	EIP1898 bool
	// DefaultTag supplies a declared optional reference before validation and
	// establishment. It applies only to one positional reference, when absent
	// or nil; other methods retain their existing parameter behavior.
	DefaultTag string
}

// PendingMode declares a method's pending handling at one position.
type PendingMode string

const (
	PendingNone    PendingMode = ""
	PendingObserve PendingMode = "local-observation"
	PendingRefuse  PendingMode = "local-reject"
)

// PendingSpec is one method's pending declaration.
type PendingSpec struct {
	Mode PendingMode
	Pos  int
}

// ResolverKind names the resolver class a plan drives: strict and
// call-revert corroborate, while median and local sample without clustering.
type ResolverKind string

const (
	ResolverStrict     ResolverKind = "strict"
	ResolverMedian     ResolverKind = "median"
	ResolverLocal      ResolverKind = "local"
	ResolverCallRevert ResolverKind = "call-revert"
)

// corroboratingResolvers reports whether a resolver class resolves under an
// invocation's frozen corroboration snapshot, and therefore collects over the
// fixed voting basis.
func corroboratingResolvers(kind ResolverKind) bool {
	switch kind {
	case ResolverStrict, ResolverCallRevert:
		return true
	}
	return false
}

// ReadPlan is the explicit execution plan of one read method: the facts the
// fixed phase pipeline reads, and the resolver value those facts select. The
// plan carries the method-owned hooks (prepare, admit) and the declared
// reference and pending shapes; the generic rewrite, requirement, and rule
// projections below are the only code that executes them, so the declared
// facts and the executed behavior cannot drift apart.
type ReadPlan struct {
	Method string

	// Normalize is the method's standalone comparison-key function. Diagnostics
	// and public helpers use it independently of the execution route.
	Normalize func(json.RawMessage) (string, error)

	// Resolver and Admit form the raw execution contract. Canonical is the
	// alternative processed-input contract. Exactly one is executable.
	Resolver  Resolver
	Canonical *CanonicalRead
	// ResolverKind is the declared resolver class. It selects the collection
	// mode and the resolution entry point; plan validation keeps it and the
	// resolver value's own capabilities from diverging.
	ResolverKind ResolverKind

	// Reference declares where the request can ride a moving tag. A zero
	// value means the read never needs a reference.
	Reference ReferenceSpec
	// Pending declares the pending column the method-rule index records.
	Pending PendingSpec
	// PendingReject, when non-nil, is the local rejection the pipeline runs
	// in the Validate / Prepare phase: a pending spelling at the declared
	// position fails the read before any establishment or upstream contact.
	// A method whose own validate hook owns its pending rejection leaves
	// this nil; the Pending column still records the declared semantics.
	PendingReject func(rpc.Request) error

	// Prepare validates and prepares an owned request before establishment.
	// It must preserve Method and must not mutate the caller's request.
	// Later phases consume the returned request, including any frozen inputs.
	Prepare func(context.Context, rpc.Request) (rpc.Request, error)
	// Admit runs after collection with the same-source caller the
	// coordinator supplies. A nil hook admits nothing.
	Admit func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error)

	// ProjectorID names the runtime-bound observation projector. The plan
	// never carries a factory; the runtime binds the projector by method.
	ProjectorID string
}

// CanonicalRead pairs a method processor with a resolver that consumes only
// processed observations. It carries no raw admission or encoding fallback.
type CanonicalRead struct {
	Process  CanonicalProcessor
	Resolver CanonicalOutcomeResolver
}

// ValidateExecution rejects missing or mixed contracts before network work.
func (p ReadPlan) ValidateExecution() error {
	if p.Canonical != nil {
		if p.Resolver != nil || p.Admit != nil || p.Canonical.Process == nil || p.Canonical.Resolver == nil || p.ResolverKind != ResolverStrict || p.Pending.Mode == PendingObserve {
			return fmt.Errorf("rpc: invalid canonical execution for %q: %w", p.Method, rpc.ErrInvalidReadPlan)
		}
		return nil
	}
	if p.Resolver == nil {
		return fmt.Errorf("rpc: read plan for %q has no resolver: %w", p.Method, rpc.ErrInvalidReadPlan)
	}
	if p.Pending.Mode == PendingObserve && p.Normalize == nil {
		return fmt.Errorf("rpc: pending read %q has no normalizer: %w", p.Method, rpc.ErrInvalidReadPlan)
	}
	return nil
}

// Corroborating reports whether the plan resolves under an invocation's
// frozen corroboration snapshot, and therefore collects over the fixed
// voting basis. The kind — not a type assertion over a chain — is the
// declaration.
func (p ReadPlan) Corroborating() bool { return corroboratingResolvers(p.ResolverKind) }

// ApplyReferenceDefault materializes an optional positional reference without
// changing the caller's parameter slice. It is idempotent and method-agnostic.
func (p ReadPlan) ApplyReferenceDefault(req rpc.Request) rpc.Request {
	if p.Reference.DefaultTag == "" || len(p.Reference.Positions) != 1 || (p.Reference.Kind != ReferenceShapeHashAt && p.Reference.Kind != ReferenceShapeHeightAt) {
		return req
	}
	pos := p.Reference.Positions[0]
	if pos < 0 || pos > len(req.Params) || (pos < len(req.Params) && req.Params[pos] != nil) {
		return req
	}
	params := append([]any(nil), req.Params...)
	if pos == len(params) {
		params = append(params, p.Reference.DefaultTag)
	} else {
		params[pos] = p.Reference.DefaultTag
	}
	return rpc.Request{Method: req.Method, Params: params}
}

// ReferenceRequirement reports the reference this request needs: which
// moving tag it rides, and whether establishment is needed at all. The
// derivation and the rewrite in PrepareWithReference read the same
// declaration, so a position either declares and rewrites or does neither.
// Mixed moving tags report "not needed" and let the rewrite own the local
// rejection.
func (p ReadPlan) ReferenceRequirement(req rpc.Request) (rpc.ReferenceRequest, bool) {
	req = p.ApplyReferenceDefault(req)
	if p.Reference.EIP1898 {
		// A hand-assembled plan can declare EIP1898 without a position: the
		// compiled-plan validator rejects that combination, but this seam
		// owns the runtime half of the guard. With no position to carry the
		// object, no request can need an anchor here.
		if len(p.Reference.Positions) == 0 {
			return rpc.ReferenceRequest{}, false
		}
		pos := p.Reference.Positions[0]
		if pos < 0 || pos >= len(req.Params) {
			return rpc.ReferenceRequest{}, false
		}
		if obj, ok := req.Params[pos].(map[string]any); ok {
			// The object must compose before its tag can declare a need: an
			// invalid object is the rewrite's local rejection, and a read
			// that would reject locally never establishes. Explicit pins,
			// quantities, pending, and earliest need no reference.
			_, needAnchor, tag, err := rpc.ValidateEIP1898Object(req.Method, obj)
			if err != nil || !needAnchor {
				return rpc.ReferenceRequest{}, false
			}
			return rpc.ReferenceRequest{Kind: rpc.ReferenceKindForTag(tag)}, true
		}
		// A string at the position belongs to the anchored layer below.
	}
	switch p.Reference.Kind {
	case ReferenceShapeHashAt, ReferenceShapeHeightAt:
		tag, needed := singleMovingTag(req, p.Reference.Positions)
		if !needed {
			return rpc.ReferenceRequest{}, false
		}
		return rpc.ReferenceRequest{Kind: rpc.ReferenceKindForTag(tag)}, true
	case ReferenceShapeHeightFieldsAt:
		return p.fieldsRequirement(req)
	}
	return rpc.ReferenceRequest{}, false
}

// fieldsRequirement mirrors the field-anchoring declaration: a missing bound
// defaults to the default reference, an explicit moving tag names its level,
// and a blockHash pin needs no reference. Contradictory tags report "not
// needed" so the rewrite owns the rejection.
func (p ReadPlan) fieldsRequirement(req rpc.Request) (rpc.ReferenceRequest, bool) {
	if p.Reference.ParamPos < 0 || p.Reference.ParamPos >= len(req.Params) {
		return rpc.ReferenceRequest{}, false
	}
	obj, ok := req.Params[p.Reference.ParamPos].(map[string]any)
	if !ok {
		return rpc.ReferenceRequest{}, false
	}
	if _, pinned := obj["blockHash"]; pinned {
		return rpc.ReferenceRequest{}, false
	}
	tag := ""
	missing := false
	for _, field := range p.Reference.Fields {
		value, present := obj[field]
		if !present {
			// A provider's implicit default is a moving tag in field
			// clothing; the rewrite fills it with the anchor, so it does not
			// conflict with an explicit tag — it inherits whatever level the
			// explicit tags declare.
			missing = true
			continue
		}
		if value, isStr := value.(string); isStr && movingTags[strings.ToLower(value)] {
			lowered := strings.ToLower(value)
			// Same strictness as the rewrite: two different explicit tags
			// are the rewrite's local rejection, and a read that would
			// reject locally never establishes. Treating latest as a
			// wildcard here — the old reading — burned a full anchor round
			// on {fromBlock: latest, toBlock: safe} before the rewrite
			// rejected it, in an order-dependent way.
			if tag != "" && tag != lowered {
				return rpc.ReferenceRequest{}, false
			}
			tag = lowered
		}
	}
	if tag == "" {
		if !missing {
			return rpc.ReferenceRequest{}, false
		}
		tag = "latest"
	}
	return rpc.ReferenceRequest{Kind: rpc.ReferenceKindForTag(tag)}, true
}

// PrepareWithReference rewrites one request for fan-out. The established
// reference evidence arrives as a value — the plan never establishes, reads
// no context, and holds no network callback — and refOK is false when the
// read declared no moving tag. The returned pin is the block reference the
// rewrite introduced (empty for reads that rewrote nothing, including
// pending shapes), so projection records the security-relevant pin.
//
// The EIP-1898 object form is validated and rewritten before string positions are
// considered, invalid objects fail locally with ErrInvalidRequest before any
// upstream contact, and established evidence that does not serve the
// request's tag fails the read the way a missing reference would.
func (p ReadPlan) PrepareWithReference(req rpc.Request, ref rpc.ReferenceEvidence, refOK bool) (rpc.Request, string, error) {
	req = p.ApplyReferenceDefault(req)
	// The copy preserves the parameter list's shape in both directions: nil
	// stays nil (no params member on the wire), and an empty list stays
	// empty (and marshals as [] on the wire), never collapsing to nil.
	var params []any
	if req.Params != nil {
		params = make([]any, len(req.Params))
		copy(params, req.Params)
	}
	if p.Reference.EIP1898 {
		// Same empty-declaration guard as ReferenceRequirement: a
		// hand-assembled EIP1898 plan without positions carries no object,
		// so the anchored layer below sees the same absence.
		if len(p.Reference.Positions) == 0 {
			return rpc.Request{Method: req.Method, Params: params}, "", nil
		}
		pos := p.Reference.Positions[0]
		if pos >= 0 && pos < len(params) {
			if obj, ok := params[pos].(map[string]any); ok {
				return p.prepareEIP1898Object(req.Method, params, pos, obj, ref, refOK)
			}
			// Strings and anything else belong to the anchored layer below.
		}
		// An out-of-range position cannot carry an object; the anchored layer
		// below sees the same absence.
	}
	switch p.Reference.Kind {
	case ReferenceShapeHashAt, ReferenceShapeHeightAt:
		return p.prepareStringPositions(req, params, ref, refOK)
	case ReferenceShapeHeightFieldsAt:
		return p.prepareHeightFields(req, params, ref, refOK)
	}
	return rpc.Request{Method: req.Method, Params: params}, "", nil
}

// prepareEIP1898Object validates one EIP-1898 object at pos and rewrites it
// when it carries a moving blockNumber. An explicit pin, pending, earliest,
// and a quantity pass through untouched; contradictory or malformed objects
// fail locally before any upstream contact.
func (p ReadPlan) prepareEIP1898Object(method string, params []any, pos int, obj map[string]any, ref rpc.ReferenceEvidence, refOK bool) (rpc.Request, string, error) {
	pinned, needAnchor, tag, err := rpc.ValidateEIP1898Object(method, obj)
	if err != nil {
		return rpc.Request{}, "", err
	}
	if !needAnchor {
		// Explicit or pending pin: complete, untouched.
		return rpc.Request{Method: method, Params: params}, "", nil
	}
	hash, err := servingReferenceHash(ref, refOK, tag)
	if err != nil {
		return rpc.Request{}, "", err
	}
	params[pos] = rpc.EIP1898Rewrite(hash, pinned)
	return rpc.Request{Method: method, Params: params}, hash, nil
}

// prepareStringPositions rewrites moving tags at the declared string
// positions: hash methods rewrite to the anchor hash, height methods to the
// anchor height spelled as a hex quantity. Explicit numbers, hashes, and
// pending pass through untouched; mixed moving tags fail locally.
func (p ReadPlan) prepareStringPositions(req rpc.Request, params []any, ref rpc.ReferenceEvidence, refOK bool) (rpc.Request, string, error) {
	if !anyMovingTagAt(params, p.Reference.Positions) {
		return rpc.Request{Method: req.Method, Params: params}, "", nil
	}
	anchorTag, err := singleAnchorTag(req.Method, params, p.Reference.Positions)
	if err != nil {
		return rpc.Request{}, "", err
	}
	spell, err := servingReferenceSpelling(p.Reference.Kind, ref, refOK, anchorTag)
	if err != nil {
		return rpc.Request{}, "", err
	}
	for _, pos := range p.Reference.Positions {
		if pos < 0 || pos >= len(params) {
			continue
		}
		if tag, ok := params[pos].(string); ok && movingTags[strings.ToLower(tag)] {
			params[pos] = spell
		}
	}
	return rpc.Request{Method: req.Method, Params: params}, spell, nil
}

// prepareHeightFields rewrites named string fields of the object parameter at
// paramPos to the established height — and defaults them to it when absent,
// since a provider's implicit default is a moving tag in field clothing. A
// blockHash pin excludes the range fields: coexisting bounds are
// contradictory input and fail locally; a pin without range fields is
// complete and passes untouched.
func (p ReadPlan) prepareHeightFields(req rpc.Request, params []any, ref rpc.ReferenceEvidence, refOK bool) (rpc.Request, string, error) {
	pos := p.Reference.ParamPos
	if pos < 0 || pos >= len(params) {
		return rpc.Request{Method: req.Method, Params: params}, "", nil
	}
	obj, ok := params[pos].(map[string]any)
	if !ok {
		// Not an object (hash-pin form, malformed): upstream judges.
		return rpc.Request{Method: req.Method, Params: params}, "", nil
	}
	if _, pinned := obj["blockHash"]; pinned {
		for _, field := range p.Reference.Fields {
			if _, present := obj[field]; present {
				return rpc.Request{}, "", fmt.Errorf("rpc: %s mixes blockHash with %q: %w", req.Method, field, rpc.ErrInvalidRequest)
			}
		}
		return rpc.Request{Method: req.Method, Params: params}, "", nil
	}
	rewrite := false
	anchorTag := ""
	for _, field := range p.Reference.Fields {
		value, present := obj[field]
		if !present {
			rewrite = true
			break
		}
		if tag, isStr := value.(string); isStr && movingTags[strings.ToLower(tag)] {
			rewrite = true
			tag = strings.ToLower(tag)
			if anchorTag != "" && anchorTag != tag {
				return rpc.Request{}, "", fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", req.Method, anchorTag, tag, rpc.ErrInvalidRequest)
			}
			anchorTag = tag
		}
	}
	if !rewrite {
		return rpc.Request{Method: req.Method, Params: params}, "", nil
	}
	spell, err := servingReferenceSpelling(p.Reference.Kind, ref, refOK, anchorTag)
	if err != nil {
		return rpc.Request{}, "", err
	}
	filtered := make(map[string]any, len(obj))
	for key, value := range obj {
		filtered[key] = value
	}
	for _, field := range p.Reference.Fields {
		value, present := filtered[field]
		if !present {
			filtered[field] = spell
			continue
		}
		if tag, isStr := value.(string); isStr && movingTags[strings.ToLower(tag)] {
			filtered[field] = spell
		}
	}
	params[pos] = filtered
	return rpc.Request{Method: req.Method, Params: params}, spell, nil
}

// servingReferenceHash is the hash spelling of the establishment contract:
// the established evidence must serve the request's tag and be corroborated.
func servingReferenceHash(ref rpc.ReferenceEvidence, refOK bool, tag string) (string, error) {
	if !refOK {
		return "", fmt.Errorf("anchor: missing anchor function")
	}
	if err := servingReferenceError(ref, tag); err != nil {
		return "", err
	}
	return ref.Hash, nil
}

// servingReferenceSpelling is the same contract in the shape the declared
// reference kind rewrites with: hash kinds take the hash, height kinds take
// the height spelled as a hex quantity.
func servingReferenceSpelling(kind ReferenceShape, ref rpc.ReferenceEvidence, refOK bool, tag string) (string, error) {
	if !refOK {
		return "", fmt.Errorf("anchor: missing anchor function")
	}
	if err := servingReferenceError(ref, tag); err != nil {
		return "", err
	}
	if kind == ReferenceShapeHeightAt || kind == ReferenceShapeHeightFieldsAt {
		return "0x" + strconv.FormatUint(ref.Height, 16), nil
	}
	return ref.Hash, nil
}

// servingReferenceError fails a rewrite whose established evidence does not
// serve the request's moving tag, with the same errors the anchor readers
// report: a mismatched level is a missing reference, and uncorroborated
// evidence never rewrites a request.
func servingReferenceError(ref rpc.ReferenceEvidence, tag string) error {
	if !EvidenceServesTag(ref, tag) {
		return fmt.Errorf("anchor: %w", rpc.ErrReferenceRequired)
	}
	if ref.Status != rpc.Corroborated {
		return fmt.Errorf("anchor: uncorroborated reference: %w", rpc.ErrGuaranteeNotMet)
	}
	return nil
}

// EvidenceServesTag reports whether established reference evidence serves a
// read that rides tag: each level keeps its meaning, so safe never reuses
// latest's evidence and vice versa. latest rides as the default reference
// and records no tag.
func EvidenceServesTag(ref rpc.ReferenceEvidence, tag string) bool {
	kind := rpc.ReferenceKindForTag(tag)
	if kind == rpc.ReferenceLatest {
		return ref.Tag == "" || strings.EqualFold(ref.Tag, string(rpc.ReferenceLatest))
	}
	return strings.EqualFold(ref.Tag, string(kind))
}

// anyMovingTagAt reports whether one of the positions carries a moving tag.
// Out-of-range positions are skipped.
func anyMovingTagAt(params []any, positions []int) bool {
	for _, pos := range positions {
		if pos < 0 || pos >= len(params) {
			continue
		}
		if tag, ok := params[pos].(string); ok && movingTags[strings.ToLower(tag)] {
			return true
		}
	}
	return false
}

// singleAnchorTag names the one moving tag present at the positions, or
// rejects two different tags: mixed moving tags are a request error, never
// an interchange.
func singleAnchorTag(method string, params []any, positions []int) (string, error) {
	anchorTag := ""
	for _, pos := range positions {
		if pos < 0 || pos >= len(params) {
			continue
		}
		if tag, ok := params[pos].(string); ok && movingTags[strings.ToLower(tag)] {
			lowered := strings.ToLower(tag)
			if anchorTag != "" && anchorTag != lowered {
				return "", fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", method, anchorTag, lowered, rpc.ErrInvalidRequest)
			}
			anchorTag = lowered
		}
	}
	return anchorTag, nil
}

// PendingRejectAt returns the standard local pending rejection for one
// parameter position, failing the read before reference establishment or
// an upstream call. Methods that observe pending instead never install it.
func PendingRejectAt(pos int, method string) func(rpc.Request) error {
	return func(req rpc.Request) error {
		if pos < 0 || pos >= len(req.Params) {
			return nil
		}
		if rpc.IsPendingReference(req.Params[pos]) {
			return fmt.Errorf("rpc: pending is not supported for %s: %w", method, rpc.ErrInvalidRequest)
		}
		return nil
	}
}

// MethodRule projects the method-rule index row from the plan's declared
// facts and the resolver's own rule. The resolver fills Resolver,
// CompareKind, KeySource, and Result; the plan fills Anchor, BlockRef, and
// Pending from its declarations.
func (p ReadPlan) MethodRule() MethodRule {
	rule := MethodRule{}
	if p.Canonical != nil {
		if source, ok := p.Canonical.Resolver.(MethodRuleSource); ok {
			rule = source.MethodRule()
		}
	} else if source, ok := p.Resolver.(MethodRuleSource); ok {
		rule = source.MethodRule()
	}
	switch p.Reference.Kind {
	case ReferenceShapeHashAt:
		rule.Anchor = anchorRule("hash", p.Reference.Positions)
	case ReferenceShapeHeightAt:
		rule.Anchor = anchorRule("height", p.Reference.Positions)
	case ReferenceShapeHeightFieldsAt:
		rule.Anchor = anchorFieldsRule(p.Reference.ParamPos, p.Reference.Fields)
	}
	if p.Reference.EIP1898 {
		rule.BlockRef = RuleBlockRefEIP1898
	}
	switch p.Pending.Mode {
	case PendingObserve:
		rule.Pending = RulePendingLocalObservation
	case PendingRefuse:
		rule.Pending = RulePendingLocalReject
	}
	return rule
}
