package main

// Generation coverage and representation policies are build-time input.
// They do not register RPC methods or repair provider responses.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

type requestRoot struct {
	Method         string `json:"method"`
	Parameter      int    `json:"parameter"`
	Pointer        string `json:"schemaPointer,omitempty"`
	ExpectedSHA256 string `json:"selectedCompactSchemaSHA256,omitempty"`
}

// Explicit coverage selects only reviewed request models. Adding coverage
// requires a reviewed source and public model;
// every request uses the same schema-faithful object construction rules.
var requestRoots = []requestRoot{
	{Method: "eth_call", Parameter: 0},
	{Method: "eth_getBlockReceipts", Parameter: 0},
	{Method: "eth_getLogs", Parameter: 0},
	{Method: "eth_subscribe", Parameter: 1, Pointer: "/oneOf/2", ExpectedSHA256: "d2e56b0d79a7b0c5d2a7bf9c501354414a12c33ceb37c2b76bb556826a6a9a3d"},
	{Method: "eth_simulateV1", Parameter: 0},
	{Method: "eth_simulateV1", Parameter: 1},
}

func generateRequestRoots(reg *registry, requests map[string][]executionapi.Parameter) error {
	previous := reg.strictRequests
	reg.strictRequests = true
	defer func() { reg.strictRequests = previous }()
	for _, root := range requestRoots {
		raw, path, err := selectRequestRoot(requests, root)
		if err != nil {
			return err
		}
		node, err := parseSchema(raw)
		if err != nil {
			return fmt.Errorf("spec: %s: %w", path, err)
		}
		if err := reg.registerModelNames(node, path); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, root := range requestRoots {
		key := fmt.Sprintf("%s.params[%d]%s", root.Method, root.Parameter, root.Pointer)
		if seen[key] {
			return fmt.Errorf("spec: duplicate request generation root %+v", root)
		}
		seen[key] = true
		raw, path, err := selectRequestRoot(requests, root)
		if err != nil {
			return err
		}
		node, err := parseSchema(raw)
		if err != nil {
			return fmt.Errorf("spec: %s: %w", path, err)
		}
		if _, err := reg.walk(node, path); err != nil {
			return err
		}
	}
	return nil
}

// selectRequestRoot follows an RFC 6901 JSON Pointer within one official
// parameter schema. It preserves the selected node's source bytes for audit.
func selectRequestRoot(requests map[string][]executionapi.Parameter, root requestRoot) (json.RawMessage, string, error) {
	params, ok := requests[root.Method]
	path := fmt.Sprintf("%s.params[%d]", root.Method, root.Parameter)
	if !ok || root.Parameter < 0 || root.Parameter >= len(params) {
		return nil, path, fmt.Errorf("spec: missing request generation root %s", path)
	}
	raw, err := schemaAtPointer(params[root.Parameter].Schema, root.Pointer)
	if err != nil {
		return nil, path, fmt.Errorf("spec: %s%s: %w", path, root.Pointer, err)
	}
	if root.ExpectedSHA256 != "" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, path, err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(compact.Bytes()))
		if digest != root.ExpectedSHA256 {
			return nil, path, fmt.Errorf("spec: selected node contract changed at %s%s: compact SHA-256 %s != %s", path, root.Pointer, digest, root.ExpectedSHA256)
		}
	}
	return raw, path + root.Pointer, nil
}

func schemaAtPointer(raw json.RawMessage, pointer string) (json.RawMessage, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid schema JSON")
	}
	if pointer != "" {
		if !strings.HasPrefix(pointer, "/") {
			return nil, fmt.Errorf("schema pointer must be empty or start with /")
		}
		for _, encoded := range strings.Split(pointer[1:], "/") {
			var token strings.Builder
			for i := 0; i < len(encoded); i++ {
				if encoded[i] != '~' {
					token.WriteByte(encoded[i])
					continue
				}
				i++
				if i >= len(encoded) || (encoded[i] != '0' && encoded[i] != '1') {
					return nil, fmt.Errorf("invalid JSON Pointer escape")
				}
				if encoded[i] == '0' {
					token.WriteByte('~')
				} else {
					token.WriteByte('/')
				}
			}
			key := token.String()
			switch strings.TrimSpace(string(raw))[0] {
			case '{':
				var object map[string]json.RawMessage
				if err := json.Unmarshal(raw, &object); err != nil {
					return nil, err
				}
				var found bool
				raw, found = object[key]
				if !found {
					return nil, fmt.Errorf("schema pointer member %q does not exist", key)
				}
			case '[':
				var array []json.RawMessage
				if err := json.Unmarshal(raw, &array); err != nil {
					return nil, err
				}
				index, err := strconv.Atoi(key)
				if err != nil || key != strconv.Itoa(index) || index < 0 || index >= len(array) {
					return nil, fmt.Errorf("invalid schema pointer array index %q", key)
				}
				raw = array[index]
			default:
				return nil, fmt.Errorf("schema pointer traverses a scalar")
			}
		}
	}
	if !json.Valid(raw) || len(strings.TrimSpace(string(raw))) == 0 || strings.TrimSpace(string(raw))[0] != '{' {
		return nil, fmt.Errorf("selected schema must be an object")
	}
	return raw, nil
}

// String input is checked against its effective-schema pattern first. These
// policies select only the representation of values that already passed it;
// compatibility input and schema amendments belong to their explicit owners.
var representationPolicies = map[scalarRepresentation]string{
	preserveLiteral:  "literal-v1: validate decoded input against effective-schema constraints; preserve string value",
	lowercaseAddress: "address-v2: validate decoded input against effective-schema constraints; lowercase accepted address digits",
	minimalHexByte:   "hex-byte-v2: validate decoded input against effective-schema constraints; fold one redundant leading zero",
	exactNumber:      "number-v1: finite exact JSON number, no binary floating-point rounding",
}

type scalarPolicy struct {
	Name       string `json:"name"`
	Pattern    string `json:"inputPattern,omitempty"`
	Normalizer string `json:"normalizer,omitempty"`
	Policy     string `json:"representationPolicy"`
}

type schemaPolicy struct {
	Path        string `json:"path"`
	Role        string `json:"role"`
	Pattern     string `json:"sourcePattern,omitempty"`
	Native      string `json:"native,omitempty"`
	Normalizer  string `json:"normalizer,omitempty"`
	Policy      string `json:"representationPolicy,omitempty"`
	Object      string `json:"objectPolicy,omitempty"`
	KeepStrings bool   `json:"keepStrings,omitempty"`
}

type auditRoot struct {
	Path   string `json:"path"`
	SHA256 string `json:"effectiveSchemaSHA256"`
}

type modelNamePolicy struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type generationAudit struct {
	ModelNames      []modelNamePolicy             `json:"modelNames"`
	TypeAliases     map[string]string             `json:"typeAliases"`
	Version         string                        `json:"version"`
	Errata          []executionapi.AppliedErratum `json:"errata"`
	RequestRoots    []requestRoot                 `json:"requestRoots"`
	Roots           []auditRoot                   `json:"roots"`
	Scalars         []scalarPolicy                `json:"scalarPolicies"`
	Usages          []schemaPolicy                `json:"schemaPolicies"`
	GeneratedSHA256 string                        `json:"generatedSHA256"`
}

func reviewedScalarPolicies() ([]scalarPolicy, error) {
	var policies []scalarPolicy
	for _, n := range natives {
		policy, ok := representationPolicies[n.representation]
		if !ok {
			return nil, fmt.Errorf("unreviewed scalar representation %q for %s", n.representation, n.name)
		}
		normalizer := ""
		if n.representation == exactNumber {
			normalizer = "CanonicalNumber"
		}
		policies = append(policies, scalarPolicy{n.name, n.pattern, normalizer, policy})
	}
	return policies, nil
}

func writeGenerationAudit(bundle executionapi.Bundle, generated, target string) error {
	scalars, err := reviewedScalarPolicies()
	if err != nil {
		return err
	}
	audit := generationAudit{Version: bundle.Version, Errata: bundle.Errata, RequestRoots: requestRoots, Scalars: scalars, TypeAliases: modelAliases}
	for path, name := range overrideNameByPath {
		audit.ModelNames = append(audit.ModelNames, modelNamePolicy{Path: path, Name: name})
	}
	sort.Slice(audit.ModelNames, func(i, j int) bool { return audit.ModelNames[i].Path < audit.ModelNames[j].Path })
	add := func(raw json.RawMessage, path, role string) error {
		node, err := parseSchema(raw)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		audit.Roots = append(audit.Roots, auditRoot{path, hex.EncodeToString(digest[:])})
		collectSchemaPolicies(node, path, role, false, false, &audit.Usages)
		return nil
	}
	for method, raw := range bundle.Results {
		if err := add(raw, method+".result", "result"); err != nil {
			return err
		}
	}
	for _, root := range requestRoots {
		raw, path, err := selectRequestRoot(bundle.Requests, root)
		if err != nil {
			return err
		}
		if err := add(raw, path, "request"); err != nil {
			return err
		}
	}
	sort.Slice(audit.Roots, func(i, j int) bool { return audit.Roots[i].Path < audit.Roots[j].Path })
	sort.Slice(audit.Usages, func(i, j int) bool { return audit.Usages[i].Path < audit.Usages[j].Path })
	raw, err := os.ReadFile(generated)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	audit.GeneratedSHA256 = hex.EncodeToString(digest[:])
	raw, err = json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(raw, '\n'), 0644)
}

// Traverse source nodes independently of model deduplication: the audit must
// retain each method/path even when several paths share one generated type.
func collectSchemaPolicies(n *schema, path, role string, preserve, keepStrings bool, out *[]schemaPolicy) {
	keepStrings = keepStrings || len(nonNullBranches(n.branches)) > 1
	policy := schemaPolicy{Path: path, Role: role, Pattern: n.pattern, KeepStrings: keepStrings}
	if n.typeVal == "string" || n.typeVal == "number" {
		policy.Native = nativeForPattern(n.pattern)
		if n.typeVal == "number" {
			policy.Native = "Number"
		}
		if n.typeVal == "string" {
			policy.Native = stringNative(n, preserve)
		}
		for _, native := range natives {
			if native.name == policy.Native {
				policy.Policy = representationPolicies[native.representation]
				if keepStrings && native.rewritesString() {
					policy.Policy = representationPolicies[preserveLiteral]
				}
				if native.representation == exactNumber {
					policy.Normalizer = "CanonicalNumber"
				}
			}
		}
	}
	if (n.typeVal == "" || n.typeVal == "integer") && n.hasConst {
		value, _ := decodeConst(n.constVal)
		policy.Policy = "literal-v1: enforce exact declared constant"
		switch value.(type) {
		case string:
			policy.Native = "Text"
		case bool:
			policy.Native = "bool"
		case json.Number:
			policy.Native = "int"
			policy.Normalizer = "CanonicalNumber"
			policy.Policy = "integer-const-v2: compare exact JSON numeric value with the declared constant before assigning its Go int representation"
			if _, err := strconv.ParseInt(n.constVal, 10, 32); err != nil {
				policy.Native = "Number"
				policy.Policy = representationPolicies[exactNumber]
			}
		}
	}
	if n.typeVal == "object" {
		policy.Object = "exact-key declared-field result projection"
		if role == "request" {
			policy.Object = "exact-key construction; preserve additional input members"
		}
		if string(n.additionalProperties) == "false" {
			policy.Object = "exact-key construction; reject undeclared input members"
		}
	}
	if policy.Native != "" || policy.Object != "" {
		*out = append(*out, policy)
	}
	for _, field := range n.properties {
		collectSchemaPolicies(field.schema, path+".properties."+field.name, role, preserve, keepStrings, out)
	}
	for _, field := range n.patternProperties {
		collectSchemaPolicies(field.schema, path+".patternProperties."+field.name, role, preserve, keepStrings, out)
	}
	if n.items != nil {
		collectSchemaPolicies(n.items, path+".items", role, preserve || n.uniqueItems, keepStrings, out)
	}
	for i, branch := range n.branches {
		collectSchemaPolicies(branch, fmt.Sprintf("%s.%s[%d]", path, n.compKind, i), role, preserve, keepStrings, out)
	}
}

func stringNative(n *schema, preserve bool) string {
	if n.hasConst || n.hasEnum || preserve {
		return "Text"
	}
	if native := nativeForPattern(n.pattern); native != "" {
		return native
	}
	if literalOfPattern(n.pattern) != "" {
		return "Quantity"
	}
	return "Text"
}
