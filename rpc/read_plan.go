package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The read execution plan.
//
// A planned read runs through one fixed phase order, and every phase reads
// its facts from the plan value instead of walking a decorator chain:
//
//	Validate / Prepare
//	→ ReferenceRequirement
//	→ ReferenceStage (when the read needs its reference)
//	→ PrepareWithReference
//	→ Collect
//	→ Admit
//	→ Resolve Outcome
//	→ Project / Observe
//
// The phases own the default behaviors and the error rules:
//
//   - Validate and the executable pending rejection run before anything else
//     and fail the read locally: no establishment, no fan-out, decision
//     Unknown with the reference evidence captured so far.
//   - ReferenceRequirement derives from the declaration and the request
//     alone; a request without a moving tag never establishes.
//   - The reference stage is the only establishment path; a read that
//     requires a reference without a stage fails before any fan-out, and a
//     failed establishment keeps its evidence and never starts the business
//     round.
//   - PrepareWithReference rewrites the request from the established
//     evidence. Established evidence that does not serve the request's tag
//     fails the read the same way a missing reference would.
//   - Collect runs the plan's collection mode; Admit runs the plan's hook
//     with a same-source caller the coordinator supplies; resolution follows
//     the plan's resolver class and pending handling; projection records the
//     pin the rewrite produced.
//
// A plan carries no network callback: establishment data arrives as the
// established evidence value, and the same-source admission caller is an
// execution-time dependency the coordinator injects. The decorator chain
// (Policy, Decorator, the WithAnchor family) stays as the migration-era
// compatibility surface; the plan is the source of truth for the methods a
// registry drives from plans.

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
// needs a reference. See docs/design/method-spec-reference-freeze.md.
type ReferenceSpec struct {
	Kind      ReferenceShape
	Positions []int
	ParamPos  int
	Fields    []string
	// EIP1898 marks the object form {blockNumber: tag} accepted at the
	// single anchored position, rewritten to {blockHash}.
	EIP1898 bool
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

// ResolverKind names the resolver class a plan drives. The kind — not the
// number of decorators around a resolver — decides every capability the plan
// exposes: only strict and call-revert corroborate, only median and local
// sample without clustering.
type ResolverKind string

const (
	ResolverStrict     ResolverKind = "strict"
	ResolverMedian     ResolverKind = "median"
	ResolverLocal      ResolverKind = "local"
	ResolverCallRevert ResolverKind = "call-revert"
	// ResolverBlock is the strict resolver under the block wrapper: byHash
	// identity + header-only source spelling restoration.
	ResolverBlock ResolverKind = "block"
)

// corroboratingResolvers reports whether a resolver class resolves under an
// invocation's frozen corroboration snapshot, and therefore collects over the
// fixed voting basis.
func corroboratingResolvers(kind ResolverKind) bool {
	switch kind {
	case ResolverStrict, ResolverBlock, ResolverCallRevert:
		return true
	}
	return false
}

// ReadPlan is the explicit execution plan of one read method: the facts the
// fixed phase pipeline reads, and the resolver value those facts select. The
// plan carries the method-owned hooks (validate, admit) and the declared
// reference and pending shapes; the generic rewrite, requirement, and rule
// projections below are the only code that executes them, so the declared
// facts and the executed behavior cannot drift apart.
type ReadPlan struct {
	Method string

	// Resolver is the method's resolver value, built by the rpc resolver
	// constructors (Strict, Median, Local, NewCallRevertPolicy) or a method
	// resolver wrapper such as the block wrapper. It owns normalization,
	// clustering, and adjudication; the plan never wraps it in preparation
	// decorators.
	Resolver Policy
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
	PendingReject func(Request) error

	// Validate runs before any establishment. It is the method's own hook,
	// and the plan's fixed phase order is what makes it pre-anchor.
	Validate func(context.Context, Request) error
	// Admit runs after collection with the same-source caller the
	// coordinator injects. A nil hook admits nothing.
	Admit func(context.Context, Request, json.RawMessage, AdmissionCaller) (json.RawMessage, error)

	// ProjectorID names the runtime-bound observation projector. The plan
	// never carries a factory; the runtime binds the projector by method.
	ProjectorID string
}

// Corroborating reports whether the plan resolves under an invocation's
// frozen corroboration snapshot, and therefore collects over the fixed
// voting basis. The kind — not a type assertion over a chain — is the
// declaration.
func (p ReadPlan) Corroborating() bool { return corroboratingResolvers(p.ResolverKind) }

// ReferenceRequirement reports the reference this request needs: which
// moving tag it rides, and whether establishment is needed at all. The
// derivation and the rewrite in PrepareWithReference read the same
// declaration, so a position either declares and rewrites or does neither.
// Mixed moving tags report "not needed" and let the rewrite own the local
// rejection, exactly as the compat chains do.
func (p ReadPlan) ReferenceRequirement(req Request) (ReferenceRequest, bool) {
	if p.Reference.EIP1898 {
		// A hand-assembled plan can declare EIP1898 without a position: the
		// compiled-plan validator rejects that combination, but this seam
		// owns the runtime half of the guard. With no position to carry the
		// object, no request can need an anchor here.
		if len(p.Reference.Positions) == 0 {
			return ReferenceRequest{}, false
		}
		pos := p.Reference.Positions[0]
		if pos < 0 || pos >= len(req.Params) {
			return ReferenceRequest{}, false
		}
		if obj, ok := req.Params[pos].(map[string]any); ok {
			// The object must compose before its tag can declare a need: an
			// invalid object is the rewrite's local rejection, and a read
			// that would reject locally never establishes. Explicit pins,
			// quantities, pending, and earliest need no reference.
			_, needAnchor, tag, err := ValidateEIP1898Object(req.Method, obj)
			if err != nil || !needAnchor {
				return ReferenceRequest{}, false
			}
			return ReferenceRequest{Kind: ReferenceKindForTag(tag)}, true
		}
		// A string at the position belongs to the anchored layer below.
	}
	switch p.Reference.Kind {
	case ReferenceShapeHashAt, ReferenceShapeHeightAt:
		tag, needed := singleMovingTag(req, p.Reference.Positions)
		if !needed {
			return ReferenceRequest{}, false
		}
		return ReferenceRequest{Kind: ReferenceKindForTag(tag)}, true
	case ReferenceShapeHeightFieldsAt:
		return p.fieldsRequirement(req)
	}
	return ReferenceRequest{}, false
}

// fieldsRequirement mirrors the field-anchoring declaration: a missing bound
// defaults to the default reference, an explicit moving tag names its level,
// and a blockHash pin needs no reference. Contradictory tags report "not
// needed" so the rewrite owns the rejection.
func (p ReadPlan) fieldsRequirement(req Request) (ReferenceRequest, bool) {
	if p.Reference.ParamPos < 0 || p.Reference.ParamPos >= len(req.Params) {
		return ReferenceRequest{}, false
	}
	obj, ok := req.Params[p.Reference.ParamPos].(map[string]any)
	if !ok {
		return ReferenceRequest{}, false
	}
	if _, pinned := obj["blockHash"]; pinned {
		return ReferenceRequest{}, false
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
				return ReferenceRequest{}, false
			}
			tag = lowered
		}
	}
	if tag == "" {
		if !missing {
			return ReferenceRequest{}, false
		}
		tag = "latest"
	}
	return ReferenceRequest{Kind: ReferenceKindForTag(tag)}, true
}

// PrepareWithReference rewrites one request for fan-out. The established
// reference evidence arrives as a value — the plan never establishes, reads
// no context, and holds no network callback — and refOK is false when the
// read declared no moving tag. The returned pin is the block reference the
// rewrite introduced (empty for reads that rewrote nothing, including
// pending shapes), so projection records the security-relevant pin without
// walking decorators.
//
// The rewrite reproduces the compat chains' contract exactly: the EIP-1898
// object form is validated and rewritten before the string positions are
// considered, invalid objects fail locally with ErrInvalidRequest before any
// upstream contact, and established evidence that does not serve the
// request's tag fails the read the way a missing reference would.
func (p ReadPlan) PrepareWithReference(req Request, ref ReferenceEvidence, refOK bool) (Request, string, error) {
	// The copy preserves the parameter list's shape: an empty list stays
	// empty (and marshals as [] on the wire), never collapses to nil.
	params := make([]any, len(req.Params))
	copy(params, req.Params)
	if p.Reference.EIP1898 {
		// Same empty-declaration guard as ReferenceRequirement: a
		// hand-assembled EIP1898 plan without positions carries no object,
		// so the anchored layer below sees the same absence.
		if len(p.Reference.Positions) == 0 {
			return Request{Method: req.Method, Params: params}, "", nil
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
	return Request{Method: req.Method, Params: params}, "", nil
}

// prepareEIP1898Object validates one EIP-1898 object at pos and rewrites it
// when it carries a moving blockNumber. An explicit pin, pending, earliest,
// and a quantity pass through untouched; contradictory or malformed objects
// fail locally before any upstream contact.
func (p ReadPlan) prepareEIP1898Object(method string, params []any, pos int, obj map[string]any, ref ReferenceEvidence, refOK bool) (Request, string, error) {
	pinned, needAnchor, tag, err := ValidateEIP1898Object(method, obj)
	if err != nil {
		return Request{}, "", err
	}
	if !needAnchor {
		// Explicit or pending pin: complete, untouched.
		return Request{Method: method, Params: params}, "", nil
	}
	hash, err := servingReferenceHash(ref, refOK, tag)
	if err != nil {
		return Request{}, "", err
	}
	params[pos] = EIP1898Rewrite(hash, pinned)
	return Request{Method: method, Params: params}, hash, nil
}

// prepareStringPositions rewrites moving tags at the declared string
// positions: hash methods rewrite to the anchor hash, height methods to the
// anchor height spelled as a hex quantity. Explicit numbers, hashes, and
// pending pass through untouched; mixed moving tags fail locally.
func (p ReadPlan) prepareStringPositions(req Request, params []any, ref ReferenceEvidence, refOK bool) (Request, string, error) {
	if !anyMovingTagAt(params, p.Reference.Positions) {
		return Request{Method: req.Method, Params: params}, "", nil
	}
	anchorTag, err := singleAnchorTag(req.Method, params, p.Reference.Positions)
	if err != nil {
		return Request{}, "", err
	}
	spell, err := servingReferenceSpelling(p.Reference.Kind, ref, refOK, anchorTag)
	if err != nil {
		return Request{}, "", err
	}
	for _, pos := range p.Reference.Positions {
		if pos < 0 || pos >= len(params) {
			continue
		}
		if tag, ok := params[pos].(string); ok && movingTags[strings.ToLower(tag)] {
			params[pos] = spell
		}
	}
	return Request{Method: req.Method, Params: params}, spell, nil
}

// prepareHeightFields rewrites named string fields of the object parameter at
// paramPos to the established height — and defaults them to it when absent,
// since a provider's implicit default is a moving tag in field clothing. A
// blockHash pin excludes the range fields: coexisting bounds are
// contradictory input and fail locally; a pin without range fields is
// complete and passes untouched.
func (p ReadPlan) prepareHeightFields(req Request, params []any, ref ReferenceEvidence, refOK bool) (Request, string, error) {
	pos := p.Reference.ParamPos
	if pos < 0 || pos >= len(params) {
		return Request{Method: req.Method, Params: params}, "", nil
	}
	obj, ok := params[pos].(map[string]any)
	if !ok {
		// Not an object (hash-pin form, malformed): upstream judges.
		return Request{Method: req.Method, Params: params}, "", nil
	}
	if _, pinned := obj["blockHash"]; pinned {
		for _, field := range p.Reference.Fields {
			if _, present := obj[field]; present {
				return Request{}, "", fmt.Errorf("rpc: %s mixes blockHash with %q: %w", req.Method, field, ErrInvalidRequest)
			}
		}
		return Request{Method: req.Method, Params: params}, "", nil
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
				return Request{}, "", fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", req.Method, anchorTag, tag, ErrInvalidRequest)
			}
			anchorTag = tag
		}
	}
	if !rewrite {
		return Request{Method: req.Method, Params: params}, "", nil
	}
	spell, err := servingReferenceSpelling(p.Reference.Kind, ref, refOK, anchorTag)
	if err != nil {
		return Request{}, "", err
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
	return Request{Method: req.Method, Params: params}, spell, nil
}

// servingReferenceHash is the hash spelling of the establishment contract:
// the established evidence must serve the request's tag and be corroborated.
func servingReferenceHash(ref ReferenceEvidence, refOK bool, tag string) (string, error) {
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
func servingReferenceSpelling(kind ReferenceShape, ref ReferenceEvidence, refOK bool, tag string) (string, error) {
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
func servingReferenceError(ref ReferenceEvidence, tag string) error {
	if !EvidenceServesTag(ref, tag) {
		return fmt.Errorf("anchor: %w", ErrReferenceRequired)
	}
	if ref.Status != Corroborated {
		return fmt.Errorf("anchor: uncorroborated reference: %w", ErrConflict)
	}
	return nil
}

// EvidenceServesTag reports whether established reference evidence serves a
// read that rides tag: each level keeps its meaning, so safe never reuses
// latest's evidence and vice versa. latest rides as the default reference
// and records no tag.
func EvidenceServesTag(ref ReferenceEvidence, tag string) bool {
	kind := ReferenceKindForTag(tag)
	if kind == ReferenceLatest {
		return ref.Tag == "" || strings.EqualFold(ref.Tag, string(ReferenceLatest))
	}
	return strings.EqualFold(ref.Tag, string(kind))
}

// anyMovingTagAt reports whether one of the positions carries a moving tag.
// Out-of-range positions are skipped, as in the compat chains.
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
				return "", fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", method, anchorTag, lowered, ErrInvalidRequest)
			}
			anchorTag = lowered
		}
	}
	return anchorTag, nil
}

// PendingRejectAt returns the standard local pending rejection for one
// parameter position: the same pending spellings the RejectPending
// compatibility decorator matches, failing the read before any anchor or
// upstream call. Methods that observe pending instead never install it.
func PendingRejectAt(pos int, method string) func(Request) error {
	return func(req Request) error {
		if pos < 0 || pos >= len(req.Params) {
			return nil
		}
		if IsPendingReference(req.Params[pos]) {
			return fmt.Errorf("rpc: pending is not supported for %s: %w", method, ErrInvalidRequest)
		}
		return nil
	}
}

// MethodRule projects the method-rule index row from the plan's declared
// facts and the resolver's own rule. The resolver fills Resolver,
// CompareKind, KeySource, and Result; the plan fills Anchor, BlockRef, and
// Pending from its declarations. Nothing walks decorators to build the row.
func (p ReadPlan) MethodRule() MethodRule {
	rule := MethodRule{}
	if source, ok := p.Resolver.(MethodRuleSource); ok {
		rule, _ = source.MethodRule()
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
