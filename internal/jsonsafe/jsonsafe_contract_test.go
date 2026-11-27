package jsonsafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestValidateMatchesFrozenReference(t *testing.T) {
	for _, fixture := range validationBenchmarkCases() {
		t.Run(fixture.name, func(t *testing.T) {
			err := assertValidationMatchesReference(t, fixture.raw)
			if (err == nil) != fixture.valid {
				t.Fatalf("Validate() = %v, want valid=%v", err, fixture.valid)
			}
		})
	}
	for _, raw := range validationContractInputs() {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			_ = assertValidationMatchesReference(t, raw)
		})
	}
}

func TestValidateDecodedObjectKeys(t *testing.T) {
	for _, fixture := range []struct {
		name string
		raw  string
		key  string
	}{
		{name: "literal", raw: `{"x":1,"x":2}`, key: "x"},
		{name: "escaped ASCII", raw: `{"x":1,"\u0078":2}`, key: "x"},
		{name: "escaped slash", raw: `{"a/b":1,"a\/b":2}`, key: "a/b"},
		{name: "escaped control", raw: `{"\n":1,"\u000a":2}`, key: "\n"},
		{name: "surrogate pair", raw: `{"😀":1,"\ud83d\ude00":2}`, key: "😀"},
		{name: "unpaired surrogate", raw: `{"\ud800":1,"\ufffd":2}`, key: "\ufffd"},
		{name: "two unpaired surrogates", raw: `{"\ud800":1,"\udfff":2}`, key: "\ufffd"},
		{name: "invalid UTF8", raw: "{\"\xff\":1,\"\ufffd\":2}", key: "\ufffd"},
		{name: "distinct invalid UTF8", raw: "{\"\xff\":1,\"\xfe\":2}", key: "\ufffd"},
		{name: "multiple invalid bytes", raw: "{\"\xc0\xaf\":1,\"\ufffd\ufffd\":2}", key: "\ufffd\ufffd"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			err := assertValidationMatchesReference(t, []byte(fixture.raw))
			want := fmt.Sprintf("jsonsafe: duplicate object key: %q", fixture.key)
			if !errors.Is(err, ErrDuplicateKey) || err.Error() != want {
				t.Fatalf("Validate() = %v, want %s", err, want)
			}
		})
	}
	for _, raw := range []string{
		`{"a":{"x":1},"b":{"x":2}}`,
		`[{"x":1},{"\u0078":2}]`,
		`{"é":1,"e\u0301":2}`,
		`{"\u0000":1,"":2}`,
	} {
		if err := assertValidationMatchesReference(t, []byte(raw)); err != nil {
			t.Fatalf("distinct keys or independent object scopes rejected: %q: %v", raw, err)
		}
	}
}

func TestValidateResourceBoundariesAndErrorPriority(t *testing.T) {
	depth32 := strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32)
	depth33 := strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33)
	keys4096 := validationObjectKeys(4096)
	keys4097 := validationObjectKeys(4097)
	duplicateAtLimit := string(keys4096[:len(keys4096)-1]) + `,"\u006bey0":0}`
	tooManyBeforeBadValue := string(keys4096[:len(keys4096)-1]) + `,"overflow":?}`
	for _, fixture := range []struct {
		name string
		raw  string
		want error
	}{
		{name: "32 containers", raw: depth32},
		{name: "33 containers", raw: depth33, want: ErrTooDeep},
		{name: "32 object containers", raw: strings.Repeat(`{"x":`, 32) + "0" + strings.Repeat("}", 32)},
		{name: "33 object containers", raw: strings.Repeat(`{"x":`, 33) + "0" + strings.Repeat("}", 33), want: ErrTooDeep},
		{name: "4096 keys", raw: string(keys4096)},
		{name: "4097 keys", raw: string(keys4097), want: ErrTooManyKeys},
		{name: "duplicate before key count", raw: duplicateAtLimit, want: ErrDuplicateKey},
		{name: "key count before malformed value", raw: tooManyBeforeBadValue, want: ErrTooManyKeys},
		{name: "duplicate before later syntax", raw: `{"x":1,"x":?}`, want: ErrDuplicateKey},
		{name: "duplicate before trailing data", raw: `{"x":1,"x":2} []`, want: ErrDuplicateKey},
		{name: "depth before later syntax", raw: strings.Repeat("[", 33) + "?", want: ErrTooDeep},
		{name: "depth before duplicate", raw: strings.Repeat("[", 32) + `{"x":1,"x":2}` + strings.Repeat("]", 32), want: ErrTooDeep},
		{name: "trailing empty object", raw: `null {}`, want: ErrTrailingData},
		{name: "trailing syntax", raw: `null ?`, want: ErrTrailingData},
		{name: "trailing huge number", raw: `null 1e400`, want: ErrTrailingData},
		{name: "trailing duplicate ignored as trailing", raw: `null {"x":1,"x":2}`, want: ErrTrailingData},
		{name: "empty", raw: "", want: io.EOF},
		{name: "JSON whitespace only", raw: " \r\n\t", want: io.EOF},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			err := assertValidationMatchesReference(t, []byte(fixture.raw))
			if !errors.Is(err, fixture.want) {
				t.Fatalf("Validate() = %v, want %v", err, fixture.want)
			}
		})
	}
	for _, raw := range []string{`? {"x":1,"x":2}`, `{"x":?,"x":1}`} {
		err := assertValidationMatchesReference(t, []byte(raw))
		var syntax *json.SyntaxError
		if !errors.As(err, &syntax) || errors.Is(err, ErrDuplicateKey) {
			t.Fatalf("syntax before duplicate = %v, want SyntaxError", err)
		}
	}
}

func TestValidateOriginalByteLimit(t *testing.T) {
	// The input is valid JSON at the exact effective boundary. Trailing JSON
	// whitespace counts toward the original length and cannot be trimmed away.
	raw := bytes.Repeat([]byte{' '}, referenceMaxBodyBytes+1)
	raw[0] = '0'
	if err := assertValidationMatchesReference(t, raw[:referenceMaxBodyBytes]); err != nil {
		t.Fatalf("exact byte boundary rejected: %v", err)
	}
	if err := assertValidationMatchesReference(t, raw); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one byte over boundary = %v, want ErrTooLarge", err)
	}
	raw[0] = '?'
	if err := assertValidationMatchesReference(t, raw); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size must precede syntax: %v", err)
	}
}

func TestValidateNumbersWhitespaceAndStrings(t *testing.T) {
	for _, raw := range []string{
		`0`, `-0`, `1e400`, `-1e-4000`, `9007199254740993123456789`,
		" \r\n\t {\"x\":1} \t\n\r ", `"<>&\u2028\u2029"`,
		`"\ud800"`, `"\udfff"`, `"\ud83d\ude00"`, "\"\xff\"",
	} {
		if err := assertValidationMatchesReference(t, []byte(raw)); err != nil {
			t.Fatalf("valid JSON rejected: %q: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`01`, `+1`, `.1`, `1.`, `1e`, `NaN`, `Infinity`,
		"\u00a0null", "null\u00a0", "\vnull", "null\f", "\ufeffnull",
		"\"\x01\"", `"\x41"`, `"\u00xx"`,
	} {
		if err := assertValidationMatchesReference(t, []byte(raw)); err == nil {
			t.Fatalf("invalid JSON accepted: %q", raw)
		}
	}
}

func TestDecodeMatchesFrozenReferenceAndStandardTypes(t *testing.T) {
	factories := []struct {
		name string
		new  func() any
	}{
		{name: "any", new: func() any { var value any; return &value }},
		{name: "raw fields", new: func() any { value := map[string]json.RawMessage{"before": json.RawMessage(`true`)}; return &value }},
		{name: "typed integer", new: func() any {
			return &struct {
				N     int    `json:"n"`
				Label string `json:"label"`
			}{N: 7, Label: "before"}
		}},
		{name: "integer slice", new: func() any { value := []int{7}; return &value }},
		{name: "number", new: func() any { value := json.Number("7"); return &value }},
		{name: "invalid nil destination", new: func() any { return nil }},
	}
	for _, raw := range validationContractInputs() {
		for _, factory := range factories {
			t.Run(fmt.Sprintf("%q/%s", raw, factory.name), func(t *testing.T) {
				want, got := factory.new(), factory.new()
				wantErr := decodeReference(raw, want)
				gotErr := Decode(raw, got)
				assertValidationErrorEqual(t, gotErr, wantErr)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("decoded destination = %#v, reference = %#v", got, want)
				}
			})
		}
	}
	var huge any
	err := Decode([]byte(`1e400`), &huge)
	var overflow *json.UnmarshalTypeError
	if !errors.As(err, &overflow) || overflow.Type != reflect.TypeFor[float64]() || overflow.Value != "number 1e400" {
		t.Fatalf("Decode huge number = %v, want standard float64 overflow", err)
	}
	value := struct{ X int }{X: 7}
	if err := Decode([]byte(`{"X":1,"X":2}`), &value); !errors.Is(err, ErrDuplicateKey) || value.X != 7 {
		t.Fatalf("validation failure mutated destination: %+v, %v", value, err)
	}
}

func FuzzJSONValidationMatchesFrozenReference(f *testing.F) {
	for _, raw := range validationContractInputs() {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		_ = assertValidationMatchesReference(t, raw)
		var want, got any
		wantErr := decodeReference(raw, &want)
		gotErr := Decode(raw, &got)
		assertValidationErrorEqual(t, gotErr, wantErr)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("decoded value = %#v, reference = %#v", got, want)
		}
	})
}

func validationContractInputs() [][]byte {
	inputs := []string{
		`null`, `true`, `false`, `0`, `-0`, `1`, `1.0`, `1e400`, `900719925474099312345`,
		`"plain"`, `"\"\\\/\b\f\n\r\t"`, `"\ud800"`, `"\ud83d\ude00"`, "\"\xff\"",
		`[]`, `{}`, `[null,true,1,{},[]]`, `{"n":1,"label":"after"}`, `{"n":"wrong","label":"after"}`,
		`{"x":1,"x":2}`, `{"x":1,"\u0078":2}`, "{\"\xff\":1,\"\ufffd\":2}",
		`{"a":{"x":1},"b":{"x":2}}`, " \r\n\t[0]\t\n ",
		``, ` `, `?`, `{`, `{"x":`, `{"x":?,"x":2}`, `{"x":1,"x":?}`,
		`[]]`, `null null`, `null 1e400`, `null ?`, "null\u00a0", "\u00a0null", `01`, `1e`,
		strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32),
		strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33),
	}
	result := make([][]byte, len(inputs))
	for i, raw := range inputs {
		result[i] = []byte(raw)
	}
	return result
}

func assertValidationMatchesReference(t *testing.T, raw []byte) error {
	t.Helper()
	want := validateReference(raw)
	got := Validate(raw)
	assertValidationErrorEqual(t, got, want)
	return got
}

func assertValidationErrorEqual(t *testing.T, got, want error) {
	t.Helper()
	// Deep comparison retains SyntaxError offsets, typed decode details, and
	// wrapped causes as well as the accept/reject decision and error text.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("error = %T %v, reference = %T %v", got, got, want, want)
	}
	for _, class := range []error{ErrTooLarge, ErrTooDeep, ErrDuplicateKey, ErrTooManyKeys, ErrTrailingData, io.EOF, io.ErrUnexpectedEOF} {
		if errors.Is(got, class) != errors.Is(want, class) {
			t.Fatalf("error class %v differs: got %v, reference %v", class, got, want)
		}
	}
}
