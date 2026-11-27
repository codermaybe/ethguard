package rpcengine

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
)

// Resolver resolves raw observations. Standalone normalization, preparation,
// admission, and reference requirements belong to ReadPlan. The engine owns
// generic resolution; internal/methods supplies method semantics and plans,
// one file per RPC method. Resolve must cover every input with
// method judgment and relation. Outcome.ProjectSources derives default source
// and fault views; custom resolvers only supply non-source diagnostics in Faults.
type Resolver interface {
	Resolve([]rpc.Observation) (Outcome, error)
}

// ObservationProjector is an optional semantic-room capability. It turns a
// generic observation into source-bound extensions for the selected core
// value. Resolvers that do not implement it retain the existing decision
// shape.
type ObservationProjector interface {
	ProjectObservation(json.RawMessage, Evaluation) (rpc.ExtensionSet, error)
}

// CorroboratingResolver is the optional capability of a resolver whose resolution
// is adjudicated against an invocation's fixed voting basis: the aggregator
// supplies the corroboration mode and support floor captured before any
// request was issued (decision 0025), overriding construction-time options.
// Implementing it also selects the fixed-basis collection mode in the
// aggregator: every voting member is represented in the observation set,
// including members whose request a scheduling gate skipped.
type CorroboratingResolver interface {
	ResolveCorroborated(c Corroboration, obs []rpc.Observation) (Outcome, error)
}

// RPCErrorResolver is an optional capability for resolvers whose value domain
// includes JSON-RPC error bodies. Ordinary strict/local/median resolvers leave
// protocol errors out of normalization; forwarded resolvers return them as the
// authoritative single-source answer.
type RPCErrorResolver interface {
	IncludeRPCError() bool
}

// SingleSourceResolver requests ordered forwarding instead of symmetric fan-out.
type SingleSourceResolver interface{ SingleSource() bool }

// AgreementResolver identifies deterministic answers that require corroboration
// at an ethclient-shaped boundary, where callers cannot inspect Selected.
type AgreementResolver interface{ RequiresAgreement() bool }

// MethodRule records the method-rule index declarations. A resolver declares
// its value semantics; ReadPlan adds reference and pending semantics.
//
// The index is a read-only projection. Nothing in request handling reads it.
// Allowed values, and no finer classification. That phrase bounds the
// vocabulary of each column, not the key itself: the key's own internals are
// not classified here, and the one fact about a key that is recorded is
// where it came from.
//
//	Anchor:      "hash@<positions>", "height@<positions>",
//	             "height@<pos>.<field>,<field>", empty renders "none"
//	BlockRef:    "eip1898", empty renders "none"
//	Pending:     "local-observation", "local-reject", empty renders "none"
//	Resolver:    "strict", "call-revert", "median", "local"
//	CompareKind: "quantity", "object", "method-specific"
//	KeySource:   "canonical-bytes", "normalizer", empty renders "none"
//	Result:      "canonical", "raw"
//
// Pending records the method's declared pending semantics. local-observation
// is a node-local observation capped at Selected. local-reject fails locally, contacts no upstream, and does not
// touch the anchor. none means that position has no pending form.
//
// CompareKind stays a coarse class. eth_call revert-data states and any
// other comparison-key internals do not belong in the index.
//
// KeySource records where the comparison key comes from, not how the key is
// built. The key source is a declaration read straight off the assembly, the
// option the method was given, so it is a fact about the method rather than
// an internal structure of its key. canonical-bytes means the key is the
// encoder's output. normalizer means the key comes from the normalizer,
// whether or not an encoder is attached. Two cases share it: a method that
// deliberately keeps a key narrower than its value, and a method that has no
// encoder. Result tells them apart: an encoded narrow-key result is
// canonical, and an unencoded result is raw. Only Strict declares this column:
// median samples by number without clustering, local does not cluster, and
// call-revert has a multi-dimensional key, so none of them has one key source
// to report.
//
// Result records whether this method writes a canonical result. It is
// "canonical" or "raw". The index does not explain fields and does not
// generate tests. Request handling does not read it. Where the key is the
// canonical bytes, one method clusters on what it will return, so a canonical
// scalar and a canonical object result read the same. A method may also keep a
// deliberately narrower key, such as a block's number|hash, and then Result
// still says canonical while KeySource says normalizer.
type MethodRule struct {
	Anchor      string
	BlockRef    string
	Pending     string
	Resolver    string
	CompareKind string
	KeySource   string
	Result      string
}

// MethodRuleSource reports a resolver's direct declarations. It does not
// expose another resolver or participate in normalization and resolution.
type MethodRuleSource interface {
	MethodRule() MethodRule
}

const (
	RulePendingLocalObservation = "local-observation"
	RulePendingLocalReject      = "local-reject"
	RuleBlockRefEIP1898         = "eip1898"
	RuleResolverStrict          = "strict"
	RuleResolverCallRevert      = "call-revert"
	RuleResolverMedian          = "median"
	RuleResolverLocal           = "local"
	RuleCompareQuantity         = "quantity"
	RuleCompareObject           = "object"
	RuleCompareMethodSpecific   = "method-specific"
	RuleKeyCanonicalBytes       = "canonical-bytes"
	RuleKeyNormalizer           = "normalizer"
	RuleResultCanonical         = "canonical"
	RuleResultRaw               = "raw"
)
