package main

// This file holds the generator's type model: refs, object/union models, the
// registry with fingerprint dedupe, the pattern -> native mapping policy, and
// the naming policy tables. Wire contract rules concentrate here.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dave/jennifer/jen"
)

// --- Native scalar mapping: pattern -> local Go type ---
//
// Every local type routes parsing through the hand-written wire normalizers
// when one exists, so generated and protected encoders admit exactly the same
// bytes. Relaxed official patterns that no hand-written normalizer bounds
// (Quantity64/Quantity256/StorageKey) keep their official regex as the only
// additional rule. HexByte is the one deliberate canonicalization difference:
// the official schema admits 1-2 hex digits, the project ships canonical
// QUANTITY spelling (docs/design/return-value-design.md section 5).
var nativeByPattern = map[string]string{
	`^0x(0|[1-9a-f][0-9a-f]*)$`:      "Quantity",
	`^0x(0|[1-9a-f][0-9a-f]{0,15})$`: "Quantity64",
	`^0x(0|[1-9a-f][0-9a-f]{0,63})$`: "Quantity256",
	`^0x[0-9a-f]*$`:                  "Data",
	`^0x[0-9a-f]{64}$`:               "Hash",
	`^0x[0-9a-fA-F]{40}$`:            "Address",
	`^0x[0-9a-f]{512}$`:              "Bloom",
	`^0x[0-9a-f]{16}$`:               "BlockNonce",
	`^0x[0-9a-f]{0,64}$`:             "StorageKey",
	`^0x([0-9a-fA-F]?){1,2}$`:        "HexByte",
	`^[1-9][0-9]*$`:                  "DecimalString",
}

// relaxedKinds are native types whose generated parser validates against the
// official regex in addition to a shared normalizer; keys also name the
// compiled regexp variable in the generated file.
var relaxedKinds = map[string]string{
	"Quantity64":  "quantity64Pattern",
	"Quantity256": "quantity256Pattern",
	"StorageKey":  "storageKeyPattern",
}

// keywordNatives maps schema type keywords without a hex pattern.
var keywordNatives = map[string]string{
	"string":  "Text",
	"boolean": "bool",
	"number":  "Ratio",
}

// nativeParseInfo describes how a native scalar parses raw wire bytes.
type nativeParseInfo struct {
	// fn is the hand-written wire normalizer invoked as "fn(raw, extra...)".
	// Empty means the parse is regex-only or a content string.
	fn        string
	extraArgs []any  // appended after raw (e.g. 64/"hash" for fixed hex)
	regex     string // compiled-regexp variable name for relaxed kinds
	ratio     bool   // json.Number canonicalization path
}

func nativeParse(native string) nativeParseInfo {
	switch native {
	case "Quantity":
		return nativeParseInfo{fn: "CanonicalQuantity"}
	case "Quantity64":
		return nativeParseInfo{fn: "CanonicalQuantity", regex: "quantity64Pattern"}
	case "Quantity256":
		return nativeParseInfo{fn: "CanonicalQuantity", regex: "quantity256Pattern"}
	case "StorageKey":
		return nativeParseInfo{regex: "storageKeyPattern"}
	case "Data":
		return nativeParseInfo{fn: "NormalizeWireData"}
	case "Hash":
		return nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{64, "hash"}}
	case "Address":
		return nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{40, "address"}}
	case "Bloom":
		return nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{512, "logsBloom"}}
	case "BlockNonce":
		return nativeParseInfo{fn: "ValidateWireFixedHex", extraArgs: []any{16, "nonce"}}
	case "HexByte":
		return nativeParseInfo{fn: "CanonicalHexByte"}
	case "DecimalString":
		return nativeParseInfo{fn: "NormalizeDecimalString"}
	case "Ratio":
		return nativeParseInfo{fn: "CanonicalRatio", ratio: true}
	case "Text":
		return nativeParseInfo{}
	}
	panic("nativeParse: unknown native type " + native)
}

// nativeCall renders the parse call "fn(raw, extraArgs...)". All arguments
// go into one call group: a chained jen .Call opens a nested call, not a
// longer argument list.
func nativeCall(info nativeParseInfo) *jen.Statement {
	args := make([]jen.Code, 0, 1+len(info.extraArgs))
	args = append(args, jen.Id("raw"))
	for _, arg := range info.extraArgs {
		args = append(args, jen.Lit(arg))
	}
	return jen.Id(info.fn).Call(args...)
}

// nativeDoc carries the one-line contract each native scalar documents.
var nativeDoc = map[string]string{
	"Quantity":      "canonical QUANTITY: 0x plus lowercase hex, no leading zeros, zero as 0x0",
	"Quantity64":    "QUANTITY bounded to uint64 by the official simulate schema",
	"Quantity256":   "QUANTITY bounded to 16 bytes by the official schema",
	"Data":          "hex encoded bytes; leading zeros are content, so 0x00 is never 0x0",
	"Hash":          "fixed 32-byte hex word (hashes and 32-byte storage words)",
	"Address":       "20-byte hex address, lowercase, no EIP-55 checksum",
	"Bloom":         "fixed 64-byte logs bloom",
	"BlockNonce":    "fixed 8-byte header nonce",
	"StorageKey":    "up-to-32-byte hex word as the official getProof schema admits",
	"HexByte":       "1-2 hex digit value shipped as canonical QUANTITY (type, v, yParity)",
	"DecimalString": "decimal string without leading zeros (net_version)",
	"Ratio":         "decimal in [0,1] emitted as its shortest round-trip spelling",
	"Text":          "free-form string content (error reasons and friends)",
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
	kind     refKind
	native   string // refScalar: native Go name ("Quantity", "bool", ...)
	typeName string // refObject/refUnion: registry name
	elem     *ref   // refArray element
	pointer  bool   // nullable shape (oneOf [null, X])
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
		return "[]" + r.elem.codeName()
	default:
		return r.typeName
	}
}

type fieldModel struct {
	json    string // wire name
	goName  string // exported Go name
	rawName string // safe identifier inside the raw collection struct
	req     bool   // required by the official schema
	ref     *ref

	constValues []string // exact JSON literals the value is pinned to
	patterns    []string // official regexes the parsed string must match
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
}

type unionModel struct {
	name     string
	branches []branchModel
	nullable bool // a null branch participated in the official union
	from     []string
}

type registry struct {
	byFP      map[string]*objectModel
	unions    map[string]*unionModel
	objects   []*objectModel // emission order
	unionList []*unionModel
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
	for _, n := range nativeByPattern {
		r.usedNames[n] = true
	}
	for n := range map[string]bool{"bool": true, "int": true, "Ratio": true, "Text": true} {
		r.usedNames[n] = true
	}
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
	"eth_getBlockByHash.result.oneOf[1].properties.transactions.oneOf[1].items.oneOf[0].properties.authorizationList.items": "AuthorizationTuple",
	"eth_getBlockByHash.result.oneOf[1].properties.transactions":                                                            "BlockTransactions",
}

// rootOverrides force a specific root interpretation for method results whose
// official schema branches are broader than the project contract:
//   - eth_getLogs officially admits both an array of hashes and an array of
//     logs; the project canonicalizes to logs only.
//
// Values are branch selectors appended to the result path.
var rootOverrides = map[string]string{
	"eth_getLogs": "oneOf[1]",
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

// safeRawName returns the identifier used for this wire name inside the
// generated raw collection struct. The struct is decoded with encoding/json,
// so the identifiers must be exported for the json tags to apply.
func safeRawName(json string) string {
	return goFieldName(json)
}

// goFieldName converts a JSON property name to an exported Go field name.
func goFieldName(jsonName string) string {
	var b strings.Builder
	for _, p := range splitWords(jsonName) {
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	if b.Len() == 0 {
		return "Field"
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
