package main

// Generation coverage and representation policies are build-time input.
// They do not register RPC methods or repair provider responses.

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

func generateRequestRoots(reg *registry, requests map[string][]executionapi.Parameter) error {
	previous := reg.strictRequests
	reg.strictRequests = true
	defer func() { reg.strictRequests = previous }()
	for _, root := range reg.manifest.RequestRoots {
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
	for _, root := range reg.manifest.RequestRoots {
		path := root.Source.Path()
		if seen[path] {
			return fmt.Errorf("spec: duplicate request generation root %s", path)
		}
		seen[path] = true
		raw, _, err := selectRequestRoot(requests, root)
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

func selectRequestRoot(requests map[string][]executionapi.Parameter, root executionapi.SchemaSelection) (json.RawMessage, string, error) {
	raw, err := executionapi.SelectSchema(executionapi.Bundle{Requests: requests}, root)
	return raw, root.Source.Path(), err
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
	Validator  string `json:"validator"`
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

func reviewedScalarPolicies(manifest executionapi.Manifest) ([]scalarPolicy, error) {
	var policies []scalarPolicy
	seen := map[string]bool{}
	for _, n := range natives {
		id, ok := manifest.ScalarPolicies[n.name]
		if !ok || id != string(n.representation) {
			return nil, fmt.Errorf("unreviewed scalar representation %q for %s", id, n.name)
		}
		policy, ok := representationPolicies[n.representation]
		if !ok {
			return nil, fmt.Errorf("unsupported scalar representation %q", id)
		}
		seen[n.name] = true
		normalizer := ""
		if n.representation == exactNumber {
			normalizer = "CanonicalNumber"
		}
		validator := "JSON string decoding"
		if n.representation == exactNumber {
			validator = "ethcodec.CanonicalNumber: exact JSON number"
		} else if shared := sharedStrictValidator(n.pattern); shared != "" {
			validator = "ethcodec." + shared + ": shared strict ASCII"
		} else if fixedLowerHexPatternDigits(n.pattern) != 0 {
			validator = "ethcodec.IsLowerFixedHex: fixed-width ASCII"
		} else if n.pattern != "" {
			validator = "generated exact-pattern regexp"
		}
		policies = append(policies, scalarPolicy{Name: n.name, Pattern: n.pattern, Normalizer: normalizer, Validator: validator, Policy: policy})
	}
	for name := range manifest.ScalarPolicies {
		if !seen[name] {
			return nil, fmt.Errorf("unused scalar policy %s", name)
		}
	}
	return policies, nil
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
		collectSchemaPolicies(field.schema, schemaChild(path, "properties", field.name), role, preserve, keepStrings, out)
	}
	for _, field := range n.patternProperties {
		collectSchemaPolicies(field.schema, schemaChild(path, "patternProperties", field.name), role, preserve, keepStrings, out)
	}
	if n.items != nil {
		collectSchemaPolicies(n.items, schemaChild(path, "items"), role, preserve || n.uniqueItems, keepStrings, out)
	}
	for i, branch := range n.branches {
		collectSchemaPolicies(branch, schemaChild(path, n.compKind, itoa(i)), role, preserve, keepStrings, out)
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
