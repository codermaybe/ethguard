package main

// This file holds the generator's type model: refs, object/union models, the
// registry with fingerprint dedupe, the pattern -> native mapping policy, and
// the naming policy tables. Wire contract rules concentrate here.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// native describes the fixed generation-time scalar policy. Patterns validate
// canonical output, after the shared handwritten normalizer has run.
type nativeDescriptor struct {
	name, pattern, doc string
	parse              nativeParseInfo
}

type nativeParseInfo struct {
	fn        string
	extraArgs []any
	number    bool
}

var natives = []nativeDescriptor{
	{"Quantity", `^0x(0|[1-9a-f][0-9a-f]*)$`, "canonical QUANTITY, zero as 0x0", nativeParseInfo{fn: "CanonicalQuantity"}},
	{"Quantity64", `^0x(0|[1-9a-f][0-9a-f]{0,15})$`, "QUANTITY bounded to 64 bits", nativeParseInfo{fn: "CanonicalQuantity"}},
	{"Quantity256", `^0x(0|[1-9a-f][0-9a-f]{0,63})$`, "QUANTITY bounded to 256 bits (32 bytes)", nativeParseInfo{fn: "CanonicalQuantity"}},
	{"Data", `^0x[0-9a-f]*$`, "hex encoded bytes, preserving content zeros", nativeParseInfo{fn: "NormalizeWireData"}},
	{"Hash", `^0x[0-9a-f]{64}$`, "fixed 32-byte hex word", nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{64, "hash"}}},
	{"Address", `^0x[0-9a-fA-F]{40}$`, "20-byte hex address, canonically lowercase", nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{40, "address"}}},
	{"Bloom", `^0x[0-9a-f]{512}$`, "fixed 256-byte logs bloom", nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{512, "logsBloom"}}},
	{"BlockNonce", `^0x[0-9a-f]{16}$`, "fixed 8-byte header nonce", nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{16, "nonce"}}},
	{"StorageKey", `^0x[0-9a-f]{0,64}$`, "up-to-32-byte hex word", nativeParseInfo{}},
	{"HexByte", `^0x([0-9a-fA-F]?){1,2}$`, "1-2 hex digits, canonically QUANTITY", nativeParseInfo{fn: "CanonicalHexByte"}},
	{"DecimalString", `^[1-9][0-9]*$`, "positive decimal string without leading zeros", nativeParseInfo{fn: "NormalizeDecimalString"}},
	{"Number", "", "exact finite JSON number, without binary floating-point rounding", nativeParseInfo{fn: "CanonicalNumber", number: true}},
	{"Text", "", "free-form string content", nativeParseInfo{}},
}

func nativeForPattern(pattern string) string {
	for _, n := range natives {
		if n.pattern != "" && n.pattern == pattern {
			return n.name
		}
	}
	return ""
}

// --- Refs and type models ---

type refKind int

const (
	refScalar refKind = iota // named native type (or bool/int passthrough)
	refObject                // named struct
	refArray                 // inline slice of elem
	refUnion                 // named union wrapper
)

type ref struct {
	kind             refKind
	native           string // refScalar: native Go name ("Quantity", "bool", ...)
	typeName         string // refObject/refUnion: registry name
	elem             *ref   // refArray element
	pointer          bool   // nullable value (oneOf/anyOf [null, X])
	path             string
	parser           string
	pattern          string
	constant         any
	hasConst         bool
	minimum, maximum *json.Number
	inner            *ref
}

// displayName is the Go type spelled at use sites, e.g. "[]Log" or "*Receipt".
func (r *ref) displayName() string {
	if r == nil {
		return ""
	}
	base := r.codeName()
	if r.pointer {
		return "*" + base
	}
	return base
}

func (r *ref) codeName() string {
	switch r.kind {
	case refScalar:
		return r.native
	case refArray:
		return "[]" + r.elem.displayName()
	default:
		return r.typeName
	}
}

type fieldModel struct {
	json   string // wire name
	goName string // exported Go name
	req    bool   // required by the official schema
	ref    *ref
}

type objectModel struct {
	name   string
	fields []fieldModel // canonical order
	from   []string     // method context comments
}

type branchModel struct {
	goName string // union member field name
	ref    *ref
	title  string
	index  int
}

type unionModel struct {
	name     string
	branches []branchModel
	nullable bool // a null branch participated in the official union
	from     []string
	kind     string
}

type registry struct {
	byFP      map[string]*objectModel
	unions    map[string]*unionModel
	objects   []*objectModel // emission order
	unionList []*unionModel
	values    []*ref
	usedNames map[string]bool
	row       int // fallback name counter
	report    *genReport
}

func newRegistry(report *genReport) *registry {
	r := &registry{
		byFP:      map[string]*objectModel{},
		unions:    map[string]*unionModel{},
		usedNames: map[string]bool{},
		report:    report,
	}
	// Reserve native names, including every pattern-derived one.
	for _, n := range natives {
		r.usedNames[n.name] = true
	}
	r.usedNames["bool"] = true
	r.usedNames["int"] = true
	return r
}

type collision struct {
	path     string
	original string
	resolved string
}

// uniqueName resolves rename collisions against already-used names. The
// collisions it reports are expected only for context-distinct shapes
// (envelope-merged transactions versus bare ones); anything else is drift the
// release review must look at.
func (reg *registry) uniqueName(base, path string) string {
	name := base
	for i := 2; reg.usedNames[name]; i++ {
		name = fmt.Sprintf("%s%d", base, i)
		reg.report.nameCollisions = append(reg.report.nameCollisions, collision{
			path: path, original: base, resolved: name,
		})
	}
	reg.usedNames[name] = true
	return name
}

// --- Naming policy ---

// overrideNamesByTitle pins stable wire-level names for schemas the bundle
// titles with lower-case or verbose spellings.
var overrideNamesByTitle = map[string]string{
	"log":                    "Log",
	"Access list entry":      "AccessListItem",
	"Validator withdrawal":   "Withdrawal",
	"Syncing progress":       "SyncProgress",
	"Result of call failure": "SimulateCallFailure",
	"Result of call success": "SimulateCallSuccess",
	"Result of eth_simulate block-level, with array of calls": "SimulateBlock",
	"Receipt information":       "Receipt",
	"Block object":              "Block",
	"feeHistoryResults":         "FeeHistoryResult",
	"Account proof":             "AccountProofResult",
	"Transaction information":   "TransactionInformation",
	"Signed Legacy Transaction": "LegacyTransaction",
	"Signed 2930 Transaction":   "AccessListTransaction",
	"Signed 1559 Transaction":   "EIP1559Transaction",
	"Signed 4844 Transaction":   "BlobTransaction",
	"Signed 7702 Transaction":   "SetCodeTransaction",
	"Transaction hashes":        "TransactionHashes",
	"Full transactions":         "FullTransactions",
}

// overrideNameByPath pins names for schema nodes whose titles are absent or
// ambiguous: the simulate call-result error branches and unions, and the
// EIP-7702 authorization tuple (an item schema with no official title).
// Path keys are stable inside the pinned version; the emitted version
// interlock forces regeneration if the pinned bundle ever moves.
var overrideNameByPath = map[string]string{
	"eth_simulateV1.result.items.properties.calls.items":                                                                    "SimulateCallResult",
	"eth_simulateV1.result.items.properties.calls.items.oneOf[0].properties.error":                                          "SimulateCallError",
	"eth_simulateV1.result.items.properties.calls.items.oneOf[0].properties.error.oneOf[0]":                                 "SimulateErrorRevert",
	"eth_simulateV1.result.items.properties.calls.items.oneOf[0].properties.error.oneOf[1]":                                 "SimulateErrorVM",
	"eth_getBlockByHash.result.oneOf[1].properties.transactions.anyOf[1].items.oneOf[0].properties.authorizationList.items": "AuthorizationTuple",
	"eth_getBlockByHash.result.oneOf[1].properties.transactions":                                                            "BlockTransactions",
}

// fieldOrderOverrides pin the field order of objects whose canonical byte
// order is already fixed by hand-written protected encoders and golden tests.
// Everything else keeps the official document order.
var fieldOrderOverrides = map[string][]string{
	"Log": {
		"address", "topics", "data", "blockNumber",
		"transactionHash", "transactionIndex", "blockHash", "logIndex",
		"removed", "blockTimestamp",
	},
	"Receipt": {
		"transactionHash", "transactionIndex", "blockHash", "blockNumber",
		"from", "cumulativeGasUsed", "gasUsed", "effectiveGasPrice",
		"logsBloom", "logs", "status", "root", "type",
		"contractAddress", "to", "blobGasUsed", "blobGasPrice",
	},
	"AccessListResult": {"accessList", "error", "gasUsed"},
	"AccessListItem":   {"address", "storageKeys"},
	"FeeHistoryResult": {
		"oldestBlock", "baseFeePerGas", "gasUsedRatio", "reward",
		"baseFeePerBlobGas", "blobGasUsedRatio",
	},
}

// --- small naming helpers ---

var splitRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// goFieldName converts a JSON property name to an exported Go field name.
func goFieldName(jsonName string) string {
	var b strings.Builder
	for _, p := range splitWords(jsonName) {
		p = splitRe.ReplaceAllString(p, "")
		if p != "" {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	if b.Len() == 0 {
		return "Field"
	}
	if b.String()[0] >= '0' && b.String()[0] <= '9' {
		return "Field" + b.String()
	}
	return b.String()
}

// splitWords splits a wire identifier into words: explicit non-alphanumeric
// separators and camelCase humps both become boundaries.
func splitWords(s string) []string {
	var out []string
	start := 0
	runes := []rune(s)
	for i := 1; i < len(runes); i++ {
		if runes[i] == '_' || runes[i] == '-' ||
			(runes[i] >= 'A' && runes[i] <= 'Z' && runes[i-1] >= 'a' && runes[i-1] <= 'z') {
			out = append(out, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	out = append(out, strings.ToLower(string(runes[start:])))
	if len(out) == 0 {
		out = []string{strings.ToLower(s)}
	}
	return out
}

// sanitizeTitle turns a schema title into a Go identifier.
func sanitizeTitle(title string) string {
	var b strings.Builder
	for _, w := range strings.Fields(title) {
		clean := splitRe.ReplaceAllString(w, "")
		if clean == "" {
			continue
		}
		b.WriteString(strings.ToUpper(clean[:1]) + strings.ToLower(clean[1:]))
	}
	return b.String()
}
