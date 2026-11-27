package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestResultProjectionRejectsUnstableComposition(t *testing.T) {
	open := `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`
	closed := `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a"],"additionalProperties":false}`
	for _, tc := range []struct {
		name, raw, selected, competing string
	}{
		{
			// {"a":"x","extra":1} matches only the open branch, but
			// projecting extra creates a second oneOf match.
			"oneOf open and closed", `{"oneOf":[` + open + `,` + closed + `]}`,
			"example.result.oneOf[0]", "example.result.oneOf[1]",
		},
		{
			// {"a":"x","b":"kept","extra":1} selects branch 1.
			// Its output drops extra, so reparsing selects branch 0 and
			// then drops b too. This breaks both branch and byte stability.
			"anyOf negated unknown field", `{"anyOf":[
				{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"not":{"required":["extra"]}},
				{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}
			]}`,
			"example.result.anyOf[1]", "example.result.anyOf[0]",
		},
		{
			// The optional discriminator is unknown to branch 1. Losing it
			// removes branch 0's failed const check on reconstruction.
			"anyOf discarded optional discriminator", `{"anyOf":[
				{"type":"object","properties":{"a":{"type":"string"},"tag":{"const":"first"}},"required":["a"]},
				{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}
			]}`,
			"example.result.anyOf[1]", "example.result.anyOf[0]",
		},
		{
			"nested open object", `{"oneOf":[
				{"type":"object","additionalProperties":false,"properties":{"child":` + open + `},"required":["child"]},
				{"type":"object","additionalProperties":false,"properties":{"child":` + closed + `},"required":["child"]}
			]}`,
			"example.result.oneOf[0]", "example.result.oneOf[1]",
		},
		{
			"array open objects", `{"anyOf":[{"type":"array","items":` + closed + `},{"type":"array","items":` + open + `}]}`,
			"example.result.anyOf[1]", "example.result.anyOf[0]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testRegistry().walk(testSchema(t, tc.raw), "example.result")
			if err == nil || !strings.Contains(err.Error(), "unsupported result projection") || !strings.Contains(err.Error(), tc.selected+":") || !strings.Contains(err.Error(), "against "+tc.competing) {
				t.Fatalf("expected unsupported projection with both branch paths, got %v", err)
			}
			// Requests retain unknown members and need no projection proof.
			reg := testRegistry()
			reg.strictRequests = true
			if _, err := reg.walk(testSchema(t, tc.raw), "example.params[0]"); err != nil {
				t.Fatalf("lossless request composition rejected: %v", err)
			}
		})
	}
}

func TestResultProjectionUsesOnlyExactRequiredDiscriminators(t *testing.T) {
	for _, tc := range []struct {
		name, left, right string
		required, valid   bool
	}{
		{"string const", `{"const":"first"}`, `{"const":"second"}`, true, true},
		{"boolean const", `{"const":false}`, `{"const":true}`, true, true},
		{"exact number const", `{"const":9007199254740992}`, `{"const":9007199254740993}`, true, true},
		{"equivalent number const", `{"const":3}`, `{"const":3e0}`, true, false},
		{"negative zero const", `{"const":-0}`, `{"const":0}`, true, false},
		{"single literal pattern", `{"type":"string","pattern":"^0x1$"}`, `{"type":"string","pattern":"^0x2$"}`, true, true},
		{"const and pattern", `{"const":"0x1"}`, `{"type":"string","pattern":"^0x2$"}`, true, true},
		{"equivalent const and pattern", `{"const":"0x1"}`, `{"type":"string","pattern":"^0x1$"}`, true, false},
		{"optional discriminator", `{"const":"first"}`, `{"const":"second"}`, false, false},
		{"arbitrary pattern", `{"type":"string","pattern":"^first$"}`, `{"type":"string","pattern":"^second$"}`, true, false},
		{"enum sets", `{"type":"string","enum":["first"]}`, `{"type":"string","enum":["second"]}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			required := ""
			if tc.required {
				required = `,"required":["tag"]`
			}
			for _, kind := range []string{"oneOf", "anyOf"} {
				raw := fmt.Sprintf(`{"%s":[{"type":"object","properties":{"tag":%s}%s},{"type":"object","properties":{"tag":%s}%s}]}`, kind, tc.left, required, tc.right, required)
				_, err := testRegistry().walk(testSchema(t, raw), "example.result")
				if (err == nil) != tc.valid {
					t.Fatalf("%s: valid=%v, got %v", kind, tc.valid, err)
				}
			}
		})
	}
}

func TestResultProjectionAdmitsStableKindsAndFirstAnyOfBranch(t *testing.T) {
	open := `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`
	closed := `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"],"additionalProperties":false}`
	for _, raw := range []string{
		`{"oneOf":[{"type":"null"},` + open + `]}`,
		`{"oneOf":[{"type":"boolean"},` + open + `]}`,
		`{"anyOf":[` + open + `,` + closed + `]}`,
		`{"oneOf":[` + closed + `,` + closed + `]}`,
		`{"anyOf":[{"type":"array","items":{"type":"string"}},{"type":"array","items":` + open + `}]}`,
		`{"oneOf":[{"type":"array","items":` + open + `},{"type":"array","items":{"type":"string"}}]}`,
		`{"type":"object","properties":{"common":{"type":"string"}},"required":["common"],"oneOf":[
			{"type":"object","properties":{"tag":{"const":1}},"required":["tag"]},
			{"type":"object","properties":{"tag":{"const":2}},"required":["tag"]}
		]}`,
	} {
		if _, err := testRegistry().walk(testSchema(t, raw), "example.result"); err != nil {
			t.Fatalf("stable projection rejected: %s: %v", raw, err)
		}
	}
}
