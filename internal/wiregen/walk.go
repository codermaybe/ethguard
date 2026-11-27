package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// walk builds a value IR. Presence belongs only to object fields; every other
// constraint follows the value through roots, arrays and composition branches.
func (reg *registry) walk(node *schema, path string) (*ref, error) {
	if err := validateSchema(node, path); err != nil {
		return nil, err
	}
	r, err := reg.walkShape(node, path)
	if err != nil {
		return nil, err
	}
	r.path = path
	r.pattern = node.pattern
	r.minimum, r.maximum = node.minimum, node.maximum
	r.enumValues, r.hasEnum = node.enumValues, node.hasEnum
	r.minItems, r.maxItems, r.uniqueItems = node.minItems, node.maxItems, node.uniqueItems
	if node.hasConst {
		r.hasConst = true
		value, err := decodeConst(node.constVal)
		if err != nil {
			return nil, fmt.Errorf("spec: %s: invalid const: %w", path, err)
		}
		r.constant = value
	}
	if !r.pointer && (r.kind == refObject || r.kind == refUnion || r.kind == refMap) {
		r.parser = "parse" + r.typeName
		return r, nil
	}
	r.parser = fmt.Sprintf("parseValue%d", len(reg.values))
	reg.values = append(reg.values, r)
	return r, nil
}

func decodeConst(raw string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var value any
	err := dec.Decode(&value)
	return value, err
}

func validateSchema(n *schema, path string) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("spec: %s: %s", path, fmt.Sprintf(format, args...))
	}
	if n == nil {
		return fail("missing schema node")
	}
	if len(n.unsupported) != 0 {
		return fail("unsupported keyword/composition %q", n.unsupported[0])
	}
	if n.compKind != "" && n.compKind != "oneOf" && n.compKind != "anyOf" {
		return fail("unsupported composition %q", n.compKind)
	}
	if n.compKind != "" && len(n.branches) == 0 {
		return fail("empty %s", n.compKind)
	}
	if len(n.required) > 0 && n.typeVal != "object" {
		return fail("required requires object type")
	}
	if n.additionalProperties != nil && n.typeVal != "object" {
		return fail("additionalProperties requires object type")
	}

	if n.hasEnum {
		if len(n.enumValues) == 0 {
			return fail("enum must be nonempty")
		}
		if n.typeVal != "string" && n.typeVal != "boolean" && n.typeVal != "number" && n.typeVal != "null" {
			return fail("enum requires an explicit scalar type")
		}
		for _, value := range n.enumValues {
			switch value.(type) {
			case string, bool, json.Number, nil:
			default:
				return fail("compound enum members are unsupported")
			}
		}
	}
	if n.not != nil {
		if n.typeVal != "object" {
			return fail("not/required requires object type")
		}
		negated := n.not
		if negated.typeVal != "" || len(negated.properties) != 0 || len(negated.patternProperties) != 0 || negated.not != nil || negated.hasEnum || negated.hasConst || negated.pattern != "" || negated.items != nil || negated.compKind != "" || negated.additionalProperties != nil || negated.minimum != nil || negated.maximum != nil || negated.minItems != nil || negated.maxItems != nil || negated.uniqueItems || len(negated.unsupported) != 0 {
			return fail("only not with a required-name set is supported")
		}
	}
	if len(n.patternProperties) > 0 {
		if n.typeVal != "object" || len(n.properties) != 0 || len(n.required) != 0 || n.not != nil || len(n.branches) != 0 || string(n.additionalProperties) != "false" {
			return fail("patternProperties requires a closed homogeneous dictionary without fixed properties or composition")
		}
		var first string
		seen := map[string]bool{}
		for _, property := range n.patternProperties {
			if seen[property.name] {
				return fail("duplicate property pattern %q", property.name)
			}
			seen[property.name] = true
			if _, err := regexp.Compile(property.name); err != nil {
				return fail("invalid property pattern: %v", err)
			}
			current := fingerprint(property.schema)
			if first != "" && current != first {
				return fail("heterogeneous patternProperties is unsupported")
			}
			first = current
		}
	}
	if (n.minItems != nil || n.maxItems != nil || n.uniqueItems) && n.typeVal != "array" {
		return fail("array constraints require array type")
	}
	if n.minItems != nil && n.maxItems != nil && *n.minItems > *n.maxItems {
		return fail("minItems exceeds maxItems")
	}
	nulls := 0
	for i, b := range n.branches {
		if b.typeVal == "null" {
			if err := validateSchema(b, fmt.Sprintf("%s.%s[%d]", path, n.compKind, i)); err != nil {
				return err
			}
		}
		if isNullBranch(b) {
			nulls++
		}
	}
	if nulls > 1 {
		return fail("duplicate null branches are unsupported")
	}
	if n.typeVal == "object" && nulls > 0 {
		return fail("object type conflicts with nullable composition")
	}
	if n.pattern != "" {
		if _, err := regexp.Compile(n.pattern); err != nil {
			return fail("invalid pattern: %v", err)
		}
		if n.typeVal != "string" {
			return fail("pattern requires an explicit string type")
		}
	}
	if (n.minimum != nil || n.maximum != nil) && n.typeVal != "number" {
		return fail("numeric bounds require number type")
	}
	if n.hasConst {
		v, err := decodeConst(n.constVal)
		if err != nil {
			return fail("invalid const: %v", err)
		}
		switch v.(type) {
		case string:
			if n.typeVal != "" && n.typeVal != "string" {
				return fail("string const conflicts with type")
			}
		case bool:
			if n.typeVal != "" && n.typeVal != "boolean" {
				return fail("boolean const conflicts with type")
			}
		case json.Number:
			if n.typeVal != "" && n.typeVal != "integer" && n.typeVal != "number" {
				return fail("numeric const conflicts with type")
			}
			if n.typeVal == "integer" {
				if _, err := strconv.ParseInt(n.constVal, 10, 32); err != nil {
					return fail("unsupported integer const %q", n.constVal)
				}
			}
		default:
			return fail("unsupported const %s", n.constVal)
		}
		if len(n.branches) > 0 || n.items != nil || len(n.properties) > 0 {
			return fail("const on a compound value is unsupported")
		}
	}
	if len(n.branches) > 0 {
		if n.compKind == "" {
			return fail("composition kind is missing")
		}
		if n.additionalProperties != nil {
			return fail("additionalProperties on a composition is unsupported")
		}
		if n.typeVal == "object" && len(n.properties) == 0 {
			return fail("object composition without envelope properties is unsupported")
		}
		if n.pattern != "" || n.minimum != nil || n.maximum != nil || n.items != nil || n.hasEnum || n.not != nil || n.minItems != nil || n.maxItems != nil || n.uniqueItems {
			return fail("unsupported constraints on composition")
		}
		if n.typeVal != "" && n.typeVal != "object" {
			return fail("unsupported type on composition %q", n.typeVal)
		}
		if len(n.properties) > 0 {
			for i, b := range n.branches {
				if isNullBranch(b) || (b.typeVal != "object" && len(b.properties) == 0) || len(b.branches) > 0 {
					return fail("unsupported envelope branch at %s[%d]", n.compKind, i)
				}
				if string(b.additionalProperties) == "false" {
					declared := map[string]bool{}
					for _, p := range b.properties {
						declared[p.name] = true
					}
					for _, p := range n.properties {
						if !declared[p.name] {
							return fail("%s.%s[%d]: closed branch forbids envelope property %q", path, n.compKind, i, p.name)
						}
					}
				}
				for _, p := range n.properties {
					for _, q := range b.properties {
						if p.name == q.name && fingerprint(p.schema) != fingerprint(q.schema) {
							return fail("conflicting envelope property %q at %s[%d]", p.name, n.compKind, i)
						}
					}
				}
			}
		} else if len(n.required) > 0 {
			return fail("required without properties")
		}
	}
	if n.items != nil && n.typeVal != "array" {
		return fail("items requires array type")
	}
	if len(n.properties) > 0 && n.typeVal != "object" {
		return fail("properties requires object type")
	}
	return nil
}

func (reg *registry) walkShape(n *schema, path string) (*ref, error) {
	if len(n.branches) > 0 {
		content := nonNullBranches(n.branches)
		if len(content) == 0 {
			return nil, fmt.Errorf("spec: %s: all-null composition unsupported", path)
		}
		if len(content) == 1 && n.hasNullBranch() && len(n.properties) == 0 {
			inner, err := reg.walk(content[0], fmt.Sprintf("%s.%s[%d]", path, n.compKind, indexOfNonNull(n)))
			if err != nil {
				return nil, err
			}
			if inner.pointer || schemaAdmitsNull(content[0]) {
				return nil, fmt.Errorf("spec: %s: nested nullable composition unsupported", path)
			}
			out := *inner
			out.pointer = true
			out.inner = inner
			return &out, nil
		}
		return reg.unionOf(n, path)
	}
	switch n.typeVal {
	case "object":
		if reg.preserveStrings && !reg.strictRequests && string(n.additionalProperties) != "false" {
			return nil, fmt.Errorf("spec: %s: uniqueItems cannot preserve unknown members of a projected open object", path)
		}
		if len(n.patternProperties) > 0 {
			return reg.dictionaryOf(n, path)
		}
		if len(n.properties) == 0 && !reg.strictRequests {
			return nil, fmt.Errorf("spec: %s: object without properties", path)
		}
		fp := reg.modelFingerprint(n)
		if prev := reg.byFP[fp]; prev != nil {
			prev.from = appendMethod(prev.from, methodOfPath(path))
			if overrideNameByPath[path] == prev.name {
				reg.report.visited[path] = true
			}
			return &ref{kind: refObject, typeName: prev.name}, nil
		}
		fields, err := reg.fieldsOf(n, path)
		if err != nil {
			return nil, err
		}
		name, err := reg.nameFor(n, path)
		if err != nil {
			return nil, err
		}
		obj := &objectModel{name: name, fields: applyOrderOverride(name, fields), from: []string{methodOfPath(path)}, request: reg.strictRequests, closed: string(n.additionalProperties) == "false", retainAdditional: reg.strictRequests && string(n.additionalProperties) != "false"}
		for _, property := range n.properties {
			obj.propertyNames = append(obj.propertyNames, property.name)
		}
		if n.not != nil {
			obj.hasForbiddenRequired = true
			obj.forbiddenRequired = append([]string{}, n.not.required...)
		}
		reg.byFP[fp] = obj
		reg.objects = append(reg.objects, obj)
		reg.report.objects++
		reg.report.countFields += len(fields)
		return &ref{kind: refObject, typeName: name}, nil
	case "array":
		previous := reg.preserveStrings
		reg.preserveStrings = previous || n.uniqueItems
		defer func() { reg.preserveStrings = previous }()
		elem, err := reg.walk(n.items, path+".items")
		if err != nil {
			return nil, err
		}
		return &ref{kind: refArray, elem: elem}, nil
	case "boolean":
		return &ref{kind: refScalar, native: "bool"}, nil
	case "number":
		return &ref{kind: refScalar, native: "Number"}, nil
	case "string":
		// Literal constraints and uniqueItems compare the JSON string itself.
		// Composition context instead travels through private parsers, so it
		// preserves string identity without changing a shared native type.
		return &ref{kind: refScalar, native: stringNative(n, reg.preserveStrings)}, nil
	case "", "integer":
		if n.hasConst {
			v, _ := decodeConst(n.constVal)
			native := "int"
			switch v.(type) {
			case string:
				native = "Text"
			case bool:
				native = "bool"
			case json.Number:
				if _, err := strconv.ParseInt(n.constVal, 10, 32); err != nil {
					native = "Number"
				}
			}
			return &ref{kind: refScalar, native: native}, nil
		}
	}
	return nil, fmt.Errorf("spec: %s: unsupported or unconsumed type %q", path, n.typeVal)
}

func (reg *registry) fieldsOf(n *schema, path string) ([]fieldModel, error) {
	required := map[string]bool{}
	for _, name := range n.required {
		required[name] = true
	}
	fields := make([]fieldModel, 0, len(n.properties))
	names := map[string]bool{}
	goNames := map[string]bool{}
	for _, p := range n.properties {
		if names[p.name] {
			return nil, fmt.Errorf("spec: %s: duplicate property %q", path, p.name)
		}
		names[p.name] = true
		r, err := reg.walk(p.schema, path+".properties."+p.name)
		if err != nil {
			return nil, err
		}
		name := goFieldName(p.name)
		if goNames[name] || name == "MarshalJSON" || (reg.strictRequests && string(n.additionalProperties) != "false" && name == "AdditionalProperties") {
			return nil, fmt.Errorf("spec: %s.properties.%s: conflicting Go field name %q", path, p.name, name)
		}
		goNames[name] = true
		fields = append(fields, fieldModel{json: p.name, goName: name, req: required[p.name], ref: r})
	}
	for name := range required {
		if !names[name] {
			return nil, fmt.Errorf("spec: %s: required field %q has no property", path, name)
		}
	}
	return fields, nil
}

func (reg *registry) unionOf(n *schema, path string) (*ref, error) {
	if !reg.strictRequests {
		if err := validateResultProjection(n, path); err != nil {
			return nil, err
		}
	}
	key := reg.modelFingerprint(n)
	if prev := reg.unions[key]; prev != nil {
		prev.from = appendMethod(prev.from, methodOfPath(path))
		return &ref{kind: refUnion, typeName: prev.name}, nil
	}
	branches := []branchModel{}
	names := map[string]bool{}
	for i, b := range n.branches {
		if isNullBranch(b) {
			continue
		}
		branchPath := fmt.Sprintf("%s.%s[%d]", path, n.compKind, i)
		if len(n.properties) > 0 {
			b = mergeBranch(n, b)
		}
		r, err := reg.walk(b, branchPath)
		if err != nil {
			return nil, err
		}
		name := reg.branchFieldName(b, i, r)
		base := name
		for suffix := 2; names[name]; suffix++ {
			name = base + itoa(suffix)
		}
		names[name] = true
		branches = append(branches, branchModel{goName: name, ref: r, title: b.title, index: i})
	}
	name, err := reg.nameFor(n, path)
	if err != nil {
		return nil, err
	}
	um := &unionModel{name: name, branches: branches, nullable: n.hasNullBranch(), kind: n.compKind, request: reg.strictRequests, from: []string{methodOfPath(path)}}
	reg.unions[key] = um
	reg.unionList = append(reg.unionList, um)
	return &ref{kind: refUnion, typeName: name}, nil
}

func schemaAdmitsNull(n *schema) bool {
	if n.typeVal != "" {
		return n.typeVal == "null"
	}
	matches := 0
	for _, b := range n.branches {
		if schemaAdmitsNull(b) {
			matches++
		}
	}
	if n.compKind == "oneOf" {
		return matches == 1
	}
	return n.compKind == "anyOf" && matches > 0
}

func (n *schema) hasNullBranch() bool {
	for _, b := range n.branches {
		if isNullBranch(b) {
			return true
		}
	}
	return false
}
func isNullBranch(n *schema) bool {
	return n.typeVal == "null" && len(n.properties) == 0 && len(n.required) == 0 && n.additionalProperties == nil && n.items == nil && len(n.branches) == 0 && !n.hasConst && n.pattern == "" && len(n.unsupported) == 0 && n.minimum == nil && n.maximum == nil && (!n.hasEnum || enumIncludesNull(n.enumValues)) && n.not == nil && len(n.patternProperties) == 0 && n.minItems == nil && n.maxItems == nil && !n.uniqueItems
}

func enumIncludesNull(values []any) bool {
	for _, value := range values {
		if value == nil {
			return true
		}
	}
	return false
}
func nonNullBranches(list []*schema) []*schema {
	var out []*schema
	for _, b := range list {
		if !isNullBranch(b) {
			out = append(out, b)
		}
	}
	return out
}
func indexOfNonNull(n *schema) int {
	for i, b := range n.branches {
		if !isNullBranch(b) {
			return i
		}
	}
	return -1
}
func literalOfPattern(pattern string) string {
	if len(pattern) < 5 || !strings.HasPrefix(pattern, "^0x") || !strings.HasSuffix(pattern, "$") {
		return ""
	}
	body := pattern[1 : len(pattern)-1]
	if !canonicalQuantityLine(body) {
		return ""
	}
	return body
}
func canonicalQuantityLine(s string) bool {
	if !strings.HasPrefix(s, "0x") {
		return false
	}
	digits := s[2:]
	if digits == "0" {
		return true
	}
	if digits == "" || digits[0] == '0' {
		return false
	}
	for _, c := range digits {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func (reg *registry) nameFor(n *schema, path string) (string, error) {
	key := reg.modelFingerprint(n)
	name := reg.preferredNames[key]
	if explicit, ok := overrideNameByPath[path]; ok {
		reg.report.visited[path] = true
		if name != "" && name != explicit {
			return "", fmt.Errorf("spec: naming conflict at %s: %s != %s", path, name, explicit)
		}
		name = explicit
	}
	if name != "" {
		if reg.usedNames[name] {
			return "", fmt.Errorf("spec: naming conflict at %s: %s already used", path, name)
		}
		reg.usedNames[name] = true
		return name, nil
	}
	base := overrideNamesByTitle[n.title]
	if base == "" {
		base = sanitizeTitle(n.title)
	}
	if base == "" {
		reg.row++
		base = fmt.Sprintf("Result%d", reg.row)
	}
	// Reserved explicit names cannot be taken by an unrelated title fallback.
	for reg.reservedNames[base] != "" {
		base += "Model"
	}
	return reg.uniqueName(base, path), nil
}
func (reg *registry) branchFieldName(n *schema, index int, r *ref) string {
	if name := sanitizeTitle(n.title); name != "" {
		return name
	}
	if r.kind == refObject || r.kind == refUnion {
		return r.typeName
	}
	return fmt.Sprintf("Branch%d", index)
}
func mergeBranch(base, branch *schema) *schema {
	out := *branch
	out.properties = append([]prop{}, branch.properties...)
	names := map[string]bool{}
	for _, p := range branch.properties {
		names[p.name] = true
	}
	for _, p := range base.properties {
		if !names[p.name] {
			out.properties = append(out.properties, p)
		}
	}
	out.required = append(append([]string{}, base.required...), branch.required...)
	return &out
}
func applyOrderOverride(name string, fields []fieldModel) []fieldModel {
	order, ok := fieldOrderOverrides[name]
	if !ok {
		return fields
	}
	out := make([]fieldModel, 0, len(fields))
	seen := map[string]bool{}
	for _, key := range order {
		for _, f := range fields {
			if f.json == key && !seen[key] {
				out = append(out, f)
				seen[key] = true
			}
		}
	}
	for _, f := range fields {
		if !seen[f.json] {
			out = append(out, f)
		}
	}
	return out
}

// fingerprint includes every consumed semantic, but excludes presentation.
func fingerprint(n *schema) string { raw, _ := json.Marshal(canonNode(n)); return string(raw) }
func canonNode(n *schema) map[string]any {
	out := map[string]any{"type": n.typeVal, "composition": n.compKind}
	if n.pattern != "" {
		out["pattern"] = n.pattern
	}
	if n.hasConst {
		out["const"] = json.RawMessage(n.constVal)
	}
	if n.minimum != nil {
		out["minimum"] = *n.minimum
	}
	if n.maximum != nil {
		out["maximum"] = *n.maximum
	}
	if n.additionalProperties != nil {
		out["additionalProperties"] = n.additionalProperties
	}
	if len(n.unsupported) > 0 {
		out["unsupported"] = n.unsupported
	}
	if len(n.required) > 0 {
		req := append([]string{}, n.required...)
		sort.Strings(req)
		out["required"] = req
	}
	if n.items != nil {
		out["items"] = canonNode(n.items)
	}
	if len(n.branches) > 0 {
		list := []any{}
		for _, b := range n.branches {
			list = append(list, canonNode(b))
		}
		out["branches"] = list
	}
	if n.hasEnum {
		out["enum"] = n.enumValues
	}
	if n.not != nil {
		out["not"] = canonNode(n.not)
	}
	if n.minItems != nil {
		out["minItems"] = *n.minItems
	}
	if n.maxItems != nil {
		out["maxItems"] = *n.maxItems
	}
	if n.uniqueItems {
		out["uniqueItems"] = true
	}
	if len(n.patternProperties) > 0 {
		patterns := map[string]any{}
		for _, p := range n.patternProperties {
			patterns[p.name] = canonNode(p.schema)
		}
		out["patternProperties"] = patterns
	}
	if len(n.properties) > 0 {
		props := map[string]any{}
		for _, p := range n.properties {
			props[p.name] = canonNode(p.schema)
		}
		out["properties"] = props
	}
	return out
}
func itoa(i int) string { return strconv.Itoa(i) }
func methodOfPath(path string) string {
	if i := strings.IndexByte(path, '.'); i >= 0 {
		return path[:i]
	}
	return path
}
func appendMethod(list []string, method string) []string {
	for _, m := range list {
		if m == method {
			return list
		}
	}
	return append(list, method)
}

func (reg *registry) modelFingerprint(n *schema) string {
	return fmt.Sprintf("request=%t:strings=%t:%s", reg.strictRequests, reg.preserveStrings, fingerprint(n))
}

func (reg *registry) dictionaryOf(n *schema, path string) (*ref, error) {
	key := reg.modelFingerprint(n)
	if previous := reg.mapByFP[key]; previous != nil {
		return &ref{kind: refMap, typeName: previous.name}, nil
	}
	value, err := reg.walk(n.patternProperties[0].schema, path+".patternProperties."+n.patternProperties[0].name)
	if err != nil {
		return nil, err
	}
	patterns := []string{}
	for _, property := range n.patternProperties {
		patterns = append(patterns, property.name)
	}
	name, err := reg.nameFor(n, path)
	if err != nil {
		return nil, err
	}
	model := &mapModel{name: name, elem: value, patterns: patterns, path: path, request: reg.strictRequests}
	reg.mapByFP[key] = model
	reg.maps = append(reg.maps, model)
	return &ref{kind: refMap, typeName: model.name}, nil
}
