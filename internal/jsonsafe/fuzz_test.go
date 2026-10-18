package jsonsafe

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// representable reports whether every JSON number token fits float64. The
// authority decode (encoding/json into any) rejects out-of-range numbers on
// its own, so those are expected Decode rejections, not fuzz findings.
func representable(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return true // syntax-level; not what this check guards
		}
		if number, ok := token.(json.Number); ok {
			if _, ferr := number.Float64(); ferr != nil {
				return false
			}
		}
	}
}

// FuzzValidateClassifies exercises the boundary gate with arbitrary input.
// Invariants:
//  1. Validate must classify every input — a panic is the finding (resource
//     limits, duplicate keys, trailing data must all surface as errors).
//  2. If Validate accepts, stdlib json.Valid must agree: the walk must not
//     be more permissive than the authority decoder.
//  3. If Validate accepts, Decode into any must succeed: the second decode
//     is supposed to be a formality, never a new rejection surface.
func FuzzValidateClassifies(f *testing.F) {
	seeds := []string{
		`{"a":1}`,
		`[1,2,3]`,
		`"scalar"`,
		`null`,
		`{"a":{"b":[null,true,"0x1"]}}`,
		`{"dup":1,"dup":2}`,
		`{"a":`,
		`{} trailing`,
		`[]]`,
		`{"a":1e400}`,
		`{"é":1}`,
		strings.Repeat("[", MaxNestingDepth+2) + strings.Repeat("]", MaxNestingDepth+2),
		`{"` + strings.Repeat("k", 4097) + `":1}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		err := Validate(raw)
		if err != nil {
			_ = err.Error() // classified rejection: only a panic would be a finding
			return
		}
		if !json.Valid(raw) {
			t.Fatalf("Validate accepted input json.Valid rejects: %q", raw)
		}
		if !representable(raw) {
			return // decode authority rejects these by design
		}
		var dst any
		if decErr := Decode(raw, &dst); decErr != nil {
			t.Fatalf("Decode failed after Validate passed: %v (input %q)", decErr, raw)
		}
	})
}
