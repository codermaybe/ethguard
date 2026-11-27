package main

// This file holds the generator's type model: refs, object/union models, the
// registry with fingerprint dedupe, the pattern -> native mapping policy, and
// the naming policy tables. Wire contract rules concentrate here.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/executionapi"
)

// native describes the fixed generation-time scalar policy. String patterns
// validate the decoded input before its explicit representation policy runs.
type nativeDescriptor struct {
	name, pattern, doc string
	representation     scalarRepresentation
}

type scalarRepresentation string

const (
	preserveLiteral  scalarRepresentation = "literal-v1"
	lowercaseAddress scalarRepresentation = "address-v2"
	minimalHexByte   scalarRepresentation = "hex-byte-v2"
	exactNumber      scalarRepresentation = "number-v1"
)

func (n nativeDescriptor) rewritesString() bool {
	return n.representation == lowercaseAddress || n.representation == minimalHexByte
}

func nativeRewritesString(name string) bool {
	for _, n := range natives {
		if n.name == name {
			return n.rewritesString()
		}
	}
	return false
}

var natives = []nativeDescriptor{
	{"Quantity", ethcodec.QuantityPattern, "canonical QUANTITY, zero as 0x0", preserveLiteral},
	{"Quantity64", `^0x(0|[1-9a-f][0-9a-f]{0,15})$`, "QUANTITY bounded to 64 bits", preserveLiteral},
	{"Quantity256", `^0x(0|[1-9a-f][0-9a-f]{0,63})$`, "QUANTITY bounded to 256 bits (32 bytes)", preserveLiteral},
	{"Data", ethcodec.DataPattern, "hex encoded bytes, preserving content zeros", preserveLiteral},
	{"Hash", `^0x[0-9a-f]{64}$`, "fixed 32-byte hex word", preserveLiteral},
	{"Address", `^0x[0-9a-fA-F]{40}$`, "20-byte hex address", lowercaseAddress},
	{"Bloom", `^0x[0-9a-f]{512}$`, "fixed 256-byte logs bloom", preserveLiteral},
	{"BlockNonce", `^0x[0-9a-f]{16}$`, "fixed 8-byte header nonce", preserveLiteral},
	{"StorageKey", `^0x[0-9a-f]{0,64}$`, "up-to-32-byte hex word", preserveLiteral},
	{"HexByte", `^0x[0-9a-f]{1,2}$`, "1-2 lowercase hex digits", minimalHexByte},
	{"DecimalString", ethcodec.DecimalStringPattern, "nonnegative decimal string without leading zeros", preserveLiteral},
	{"Number", "", "exact finite JSON number, without binary floating-point rounding", exactNumber},
	{"Text", "", "free-form string content", preserveLiteral},
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
	refMap                   // named homogeneous pattern-keyed dictionary
)

type ref struct {
	kind               refKind
	native             string // refScalar: native Go name ("Quantity", "bool", ...)
	typeName           string // refObject/refUnion: registry name
	elem               *ref   // refArray element
	pointer            bool   // nullable value (oneOf/anyOf [null, X])
	path               string
	parser             string
	pattern            string
	constant           any
	hasConst           bool
	minimum, maximum   *json.Number
	inner              *ref
	enumValues         []any
	hasEnum            bool
	minItems, maxItems *int
	uniqueItems        bool
}

type fieldModel struct {
	json   string // wire name
	goName string // exported Go name
	req    bool   // required by the official schema
	ref    *ref
}

type objectModel struct {
	name                 string
	fields               []fieldModel // canonical order
	from                 []string     // method context comments
	forbiddenRequired    []string
	closed               bool
	propertyNames        []string
	retainAdditional     bool
	hasForbiddenRequired bool
	request              bool
}

type mapModel struct {
	name     string
	elem     *ref
	patterns []string
	path     string
	request  bool
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
	request  bool
}

type registry struct {
	manifest        executionapi.Manifest
	namesByPath     map[string]string
	usedTitles      map[string]bool
	usedOrders      map[string]bool
	byFP            map[string]*objectModel
	unions          map[string]*unionModel
	objects         []*objectModel // emission order
	unionList       []*unionModel
	maps            []*mapModel
	mapByFP         map[string]*mapModel
	strictRequests  bool
	preserveStrings bool // uniqueItems compares JSON strings before any native normalization
	values          []*ref
	preferredNames  map[string]string
	reservedNames   map[string]string
	usedNames       map[string]bool
	report          *genReport
}

func newRegistry(manifest executionapi.Manifest, report *genReport) *registry {
	r := &registry{
		manifest: manifest, namesByPath: map[string]string{}, usedTitles: map[string]bool{}, usedOrders: map[string]bool{},
		byFP:           map[string]*objectModel{},
		mapByFP:        map[string]*mapModel{},
		unions:         map[string]*unionModel{},
		usedNames:      map[string]bool{},
		preferredNames: map[string]string{},
		reservedNames:  map[string]string{},
		report:         report,
	}
	for _, rule := range manifest.Names {
		r.namesByPath[rule.Source.Path()] = rule.Name
	}
	// Reserve native names, including every pattern-derived one.
	for _, n := range natives {
		r.usedNames[n.name] = true
	}
	for alias := range manifest.Aliases {
		r.usedNames[alias] = true
	}
	r.usedNames["bool"] = true
	r.usedNames["int"] = true
	return r
}

// Public identifiers are never repaired by traversal-dependent suffixes.
func (reg *registry) uniqueName(base, path string) (string, error) {
	if reg.usedNames[base] || reg.reservedNames[base] != "" {
		return "", fmt.Errorf("spec: naming conflict at %s: %s already occupied; declare an explicit name", path, base)
	}
	reg.usedNames[base] = true
	return base, nil
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
