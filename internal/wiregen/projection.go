package main

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/codermaybe/ethguard/internal/ethcodec"
)

// Result objects project declared fields. Dropping an unknown field can make
// another branch match, changing oneOf validity or anyOf's first match. Only
// admit combinations with a small, generation-time proof of stable selection.
func validateResultProjection(n *schema, path string) error {
	branches := append([]*schema{}, n.branches...)
	if len(n.properties) > 0 {
		for i, branch := range branches {
			branches[i] = mergeBranch(n, branch)
		}
	}
	for i, selected := range branches {
		if !projectsResultFields(selected) {
			continue
		}
		for j, other := range branches {
			if i == j || (n.compKind == "anyOf" && j > i) {
				continue
			}
			if !projectionKeepsBranch(selected, other) {
				return fmt.Errorf("spec: %s: unsupported result projection: cannot prove stable branch selection against %s", schemaChild(path, n.compKind, itoa(i)), schemaChild(path, n.compKind, itoa(j)))
			}
		}
	}
	return nil
}

func projectsResultFields(n *schema) bool {
	if n == nil {
		return false
	}
	if n.typeVal == "object" && string(n.additionalProperties) != "false" {
		return true
	}
	if projectsResultFields(n.items) {
		return true
	}
	for _, properties := range [][]prop{n.properties, n.patternProperties} {
		for _, property := range properties {
			if projectsResultFields(property.schema) {
				return true
			}
		}
	}
	for _, branch := range n.branches {
		if projectsResultFields(branch) {
			return true
		}
	}
	return false
}

func projectionKeepsBranch(selected, other *schema) bool {
	if jsonKinds(selected)&jsonKinds(other) == 0 {
		return true
	}
	if selected.typeVal == "array" && other.typeVal == "array" {
		// Nonempty arrays remain excluded by their element kinds; an empty
		// array is unchanged, including all length/uniqueness constraints.
		return jsonKinds(selected.items)&jsonKinds(other.items) == 0
	}
	if selected.typeVal != "object" || other.typeVal != "object" || len(selected.branches) != 0 || len(other.branches) != 0 {
		return false
	}
	for _, left := range selected.properties {
		if !slices.Contains(selected.required, left.name) || !slices.Contains(other.required, left.name) {
			continue
		}
		a, known := scalarSingleton(left.schema)
		if !known {
			continue
		}
		for _, right := range other.properties {
			if right.name != left.name {
				continue
			}
			b, known := scalarSingleton(right.schema)
			if !known {
				continue
			}
			if number, ok := a.(json.Number); ok {
				if otherNumber, ok := b.(json.Number); ok {
					if ethcodec.CompareNumbers(number, otherNumber) != 0 {
						return true
					}
					continue
				}
			}
			if a != b {
				return true
			}
		}
	}
	return false
}

// Reuse the supported literal patterns and exact scalar constants. Arbitrary
// patterns, ranges, enum sets and object constraints are deliberately not solved.
func scalarSingleton(n *schema) (any, bool) {
	if n.hasConst {
		value, err := decodeConst(n.constVal)
		if err == nil {
			switch value.(type) {
			case string, bool, json.Number:
				return value, true
			}
		}
	}
	if n.typeVal == "string" {
		if literal := literalOfPattern(n.pattern); literal != "" {
			return literal, true
		}
	}
	return nil, false
}

// Kinds are an over-approximation: an unknown node admits every kind for this
// proof and is subsequently rejected by the ordinary schema walker.
func jsonKinds(n *schema) uint8 {
	const unknown = uint8(0xff)
	if n == nil {
		return unknown
	}
	for i, kind := range []string{"null", "boolean", "number", "string", "array", "object"} {
		if n.typeVal == kind || (kind == "number" && n.typeVal == "integer") {
			return 1 << i
		}
	}
	if len(n.branches) > 0 {
		var kinds uint8
		for _, branch := range n.branches {
			kinds |= jsonKinds(branch)
		}
		return kinds
	}
	return unknown
}
