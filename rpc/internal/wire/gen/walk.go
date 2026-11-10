package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// walk turns one parsed schema node into a ref, registering named types in
// the registry. path is the canonical bundle path; it feeds override naming
// and the generation report.
func (reg *registry) walk(node *schema, path string) (*ref, error) {
	if node == nil {
		return nil, fmt.Errorf("spec: missing schema node at %q", path)
	}

	// Composition of non-null branches: a union wrapper. Object nodes
	// carrying both properties and branches merge the base properties into
	// every branch (official envelope style). This runs before null
	// hoisting so a [null, envelope] root keeps its nullness on a union ref.
	if len(nonNullBranches(node.branches)) >= 2 {
		return reg.unionOf(node, path)
	}

	// oneOf/anyOf [null, X]: the nullable form. Exactly one content branch
	// and an otherwise bare node yields *X. The hoisted branch keeps its
	// original composition index in the path for override lookups.
	if node.hasNullBranch() {
		content := nonNullBranches(node.branches)
		switch {
		case len(content) == 0:
			return nil, fmt.Errorf("spec: %q: all-null union", path)
		case len(content) == 1:
			inner, err := reg.walk(content[0], path+".oneOf["+itoa(indexOfNonNull(node))+"]")
			if err != nil {
				return nil, err
			}
			out := *inner
			out.pointer = true
			return &out, nil
		}
	}

	switch {
	case node.typeVal == "object" || len(node.properties) > 0:
		return reg.objectOf(node, path)
	case node.typeVal == "array" || node.items != nil:
		elem, err := reg.walk(node.items, path+".items")
		if err != nil {
			return nil, err
		}
		if elem.pointer {
			// Arrays of nullable elements are not modeled; the supported
			// result surface does not use them.
			return nil, fmt.Errorf("spec: %q: array of nullable elements is unsupported", path)
		}
		return &ref{kind: refArray, elem: elem}, nil
	case node.typeVal == "boolean", node.typeVal == "number":
		return &ref{kind: refScalar, native: keywordNatives[node.typeVal]}, nil
	case node.hasConst:
		// Bare const literal (the simulate error codes carry {"const": 3}).
		native := "int"
		if strings.HasPrefix(node.constVal, `"`) {
			native = "Quantity" // string-literal consts are canonical QUANTITY
		}
		return &ref{kind: refScalar, native: native}, nil
	case node.typeVal == "string" || node.pattern != "":
		// Literal QUANTITY consts (transaction type discriminators) map to
		// Quantity; the object parse pins the exact value.
		literal := literalOfPattern(node.pattern)
		if literal != "" {
			return &ref{kind: refScalar, native: "Quantity"}, nil
		}
		if node.pattern != "" {
			named, ok := nativeByPattern[node.pattern]
			if ok {
				return &ref{kind: refScalar, native: named}, nil
			}
			if node.typeVal == "string" {
				// Unmapped string patterns (official message constraints)
				// stay Text; fieldsOf attaches the pattern check.
				return &ref{kind: refScalar, native: "Text"}, nil
			}
			return nil, fmt.Errorf("spec: %q: unmapped pattern %q", path, node.pattern)
		}
		return &ref{kind: refScalar, native: "Text"}, nil
	case node.typeVal == "":
		return nil, fmt.Errorf("spec: %q: untyped node without pattern, type, const or composition", path)
	case node.typeVal == "null":
		return nil, fmt.Errorf("spec: %q: bare null node", path)
	default:
		return nil, fmt.Errorf("spec: %q: unsupported type %q", path, node.typeVal)
	}
}

func (node *schema) hasNullBranch() bool {
	for _, b := range node.branches {
		if isNullBranch(b) {
			return true
		}
	}
	return false
}

func isNullBranch(b *schema) bool {
	return b.typeVal == "null" && len(b.properties) == 0 && b.items == nil && len(b.branches) == 0
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

// indexOfNonNull returns the position of the first non-null branch inside the
// node's composition list, or -1.
func indexOfNonNull(node *schema) int {
	for i, b := range node.branches {
		if !isNullBranch(b) {
			return i
		}
	}
	return -1
}

// literalOfPattern recognizes single-literal QUANTITY patterns (^0x0$ ... )
// and returns the literal, or "" when the pattern is not a single value.
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

// canonicalQuantityLine checks "0x" + canonical no-leading-zero hex spelling.
func canonicalQuantityLine(s string) bool {
	if !strings.HasPrefix(s, "0x") {
		return false
	}
	digits := s[2:]
	if digits == "0" {
		return true
	}
	if digits == "" {
		return false
	}
	switch digits[0] {
	case '0':
		return false
	case '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f':
	default:
		return false
	}
	for _, c := range digits {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// objectOf registers a plain object type.
func (reg *registry) objectOf(node *schema, path string) (*ref, error) {
	if len(node.properties) == 0 {
		return nil, fmt.Errorf("spec: %q: object without properties", path)
	}
	fields, err := reg.fieldsOf(node, path)
	if err != nil {
		return nil, err
	}
	return reg.registerObject(node, fields, path), nil
}

// fieldsOf resolves the canonical field list (official order), requiredness,
// and the const/pattern constraints the official schema pins per property.
func (reg *registry) fieldsOf(node *schema, path string) ([]fieldModel, error) {
	required := map[string]bool{}
	for _, name := range node.required {
		required[name] = true
	}
	out := make([]fieldModel, len(node.properties))
	for i, p := range node.properties {
		fieldRef, err := reg.walk(p.schema, path+".properties."+p.name)
		if err != nil {
			return nil, err
		}
		field := fieldModel{
			json:    p.name,
			goName:  goFieldName(p.name),
			rawName: safeRawName(p.name),
			req:     required[p.name],
			ref:     fieldRef,
		}
		// Const / pattern constraints on this property's own node.
		if p.schema.hasConst {
			field.constValues = append(field.constValues, p.schema.constVal)
		}
		if literal := literalOfPattern(p.schema.pattern); literal != "" && p.schema.typeVal == "string" {
			field.constValues = append(field.constValues, `"`+literal+`"`)
		} else if literal := literalOfPattern(p.schema.pattern); literal != "" {
			field.constValues = append(field.constValues, literal)
		}
		if p.schema.pattern != "" && literalOfPattern(p.schema.pattern) == "" {
			if p.schema.typeVal == "string" {
				if _, known := nativeByPattern[p.schema.pattern]; !known {
					field.patterns = append(field.patterns, p.schema.pattern)
				}
			}
		}
		out[i] = field
	}
	return out, nil
}

// registerObject deduplicates by canonical fingerprint; identical shapes from
// different methods share one generated type, with names resolved only for
// the first occurrence.
func (reg *registry) registerObject(node *schema, fields []fieldModel, path string) *ref {
	fp := fingerprint(node)
	if prev, ok := reg.byFP[fp]; ok {
		prev.from = appendMethod(prev.from, methodOfPath(path))
		return &ref{kind: refObject, typeName: prev.name}
	}
	name := reg.nameFor(node, path)
	obj := &objectModel{name: name, from: []string{methodOfPath(path)}}
	obj.fields = applyOrderOverride(name, fields)
	reg.report.countFields += len(obj.fields)
	reg.report.objects++
	reg.byFP[fp] = obj
	reg.objects = append(reg.objects, obj)
	return &ref{kind: refObject, typeName: name}
}

// unionOf registers a union wrapper over the non-null branches. It is the
// tagged-union home of the generated model: branch selection happens in
// declared official order, and null branches disappear into pointer-like
// membership (empty wrapper = JSON null).
func (reg *registry) unionOf(node *schema, path string) (*ref, error) {
	key := fingerprint(node)
	if prev, ok := reg.unions[key]; ok {
		prev.from = appendMethod(prev.from, methodOfPath(path))
		return &ref{kind: refUnion, typeName: prev.name}, nil
	}
	var branches []branchModel
	seen := map[string]bool{}
	for i, b := range nonNullBranches(node.branches) {
		branchPath := joinPath(path, "oneOf["+itoa(i)+"]")
		if len(node.properties) > 0 {
			// Official envelope style: base properties merge into every
			// object branch; branch definitions win on conflict.
			b = mergeBranch(node, b)
		}
		branchRef, err := reg.walk(b, branchPath)
		if err != nil {
			return nil, fmt.Errorf("spec: %q: branch %d: %w", path, i, err)
		}
		branchKey := branchRef.codeName()
		if seen[branchKey] {
			continue // official branches collapsed to one Go type
		}
		seen[branchKey] = true
		branches = append(branches, branchModel{goName: reg.branchFieldName(b, i, branchRef), ref: branchRef, title: b.title})
	}
	if len(branches) == 1 {
		out := *branches[0].ref
		return &out, nil
	}
	name := reg.nameFor(node, path)
	um := &unionModel{
		name:     name,
		branches: branches,
		nullable: node.hasNullBranch(),
		from:     []string{methodOfPath(path)},
	}
	reg.unions[key] = um
	reg.unionList = append(reg.unionList, um)
	return &ref{kind: refUnion, typeName: name}, nil
}

// nameFor resolves a named type: path override first, then title override,
// then the title itself, and a numbered fallback last.
func (reg *registry) nameFor(node *schema, path string) string {
	if n, ok := overrideNameByPath[path]; ok && !reg.usedNames[n] {
		reg.usedNames[n] = true
		reg.report.visited[path] = true
		return n
	}
	if n, ok := overrideNamesByTitle[node.title]; ok {
		return reg.uniqueName(n, path)
	}
	if base := sanitizeTitle(node.title); base != "" {
		return reg.uniqueName(base, path)
	}
	reg.row++
	return reg.uniqueName(fmt.Sprintf("Result%d", reg.row), path)
}

// branchFieldName names a union member field from its title. The wrapper
// struct field is exported, so the title becomes a PascalCase name.
// Untitled branches fall back to the branch's own type name, which keeps
// generated field names self-describing (SimulateErrorRevert etc.).
func (reg *registry) branchFieldName(b *schema, index int, ref *ref) string {
	if b.title != "" {
		if name := sanitizeTitle(b.title); name != "" {
			return name
		}
	}
	switch ref.kind {
	case refObject, refUnion:
		reg.report.visited["branch:"+ref.typeName] = true
		return ref.typeName
	}
	return fmt.Sprintf("Branch%d", index)
}

// mergeBranch unions base properties with a branch: branch definitions win,
// requiredness is the union of required lists.
func mergeBranch(base, branch *schema) *schema {
	merged := &schema{title: branch.title, typeVal: "object"}
	names := map[string]bool{}
	for _, p := range branch.properties {
		merged.properties = append(merged.properties, p)
		names[p.name] = true
	}
	for _, p := range base.properties {
		if names[p.name] {
			continue
		}
		merged.properties = append(merged.properties, p)
		names[p.name] = true
	}
	merged.required = append(append([]string{}, base.required...), branch.required...)
	return merged
}

// applyOrderOverride reorders fields per the pinned canonical order table.
func applyOrderOverride(typeName string, fields []fieldModel) []fieldModel {
	order, ok := fieldOrderOverrides[typeName]
	if !ok {
		return fields
	}
	index := map[string]int{}
	for i, f := range fields {
		index[f.json] = i
	}
	seen := map[string]bool{}
	var out []fieldModel
	for _, name := range order {
		i, ok := index[name]
		if !ok || seen[name] {
			continue // override drift is reported during emission checks
		}
		seen[name] = true
		out = append(out, fields[i])
	}
	for _, f := range fields {
		if !seen[f.json] {
			out = append(out, f)
		}
	}
	if len(out) != len(fields) {
		reg := "override asked for fields the schema does not carry"
		panic(fmt.Sprintf("fieldOrderOverrides[%s]: %s", typeName, reg))
	}
	return out
}

// --- Fingerprinting: shape identity for dedupe ---

// fingerprint returns a canonical, title-independent identity of a schema
// node: properties sort by name so identical objects from different methods
// share one generated type. Property order, titles and descriptions do not
// affect identity; pattern, const and required do.
func fingerprint(node *schema) string {
	raw, err := json.Marshal(canonNode(node))
	if err != nil {
		panic(fmt.Sprintf("fingerprint marshal: %v", err))
	}
	return string(raw)
}

func canonNode(node *schema) map[string]any {
	out := map[string]any{"type": node.typeVal}
	if node.pattern != "" {
		out["pattern"] = node.pattern
	}
	if node.hasConst {
		out["const"] = node.constVal
	}
	if len(node.required) > 0 {
		req := append([]string{}, node.required...)
		sort.Strings(req)
		out["required"] = req
	}
	if node.items != nil {
		out["items"] = canonNode(node.items)
	}
	if len(node.branches) > 0 {
		branchList := []any{}
		for _, b := range node.branches {
			branchList = append(branchList, canonNode(b))
		}
		out["branches"] = branchList
	}
	if len(node.properties) > 0 {
		names := make([]string, len(node.properties))
		props := map[string]any{}
		for i, p := range node.properties {
			names[i] = p.name
			props[p.name] = canonNode(p.schema)
		}
		sort.Strings(names)
		sortedProps := []any{}
		for _, n := range names {
			sortedProps = append(sortedProps, map[string]any{"name": n, "schema": props[n]})
		}
		out["properties"] = sortedProps
	}
	return out
}

// --- helpers ---

func joinPath(path, part string) string {
	if path == "" {
		return part
	}
	return path + "." + part
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

func methodOfPath(path string) string {
	i := strings.IndexByte(path, '.')
	if i < 0 {
		return path
	}
	return path[:i]
}

func appendMethod(list []string, method string) []string {
	for _, m := range list {
		if m == method {
			return list
		}
	}
	return append(list, method)
}
