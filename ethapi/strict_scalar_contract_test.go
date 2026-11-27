package ethapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

type strictScalarConstructor struct {
	name, sample string
	pattern      *regexp.Regexp
	parse        func(json.RawMessage) (string, error)
}

// Frozen patterns and representation rules are independent of production
// predicates and generator tables. The same cases can run on the old tree.
var strictScalarConstructors = []strictScalarConstructor{
	{"Quantity", "0x1b7a3d200", regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]*)$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewQuantity(raw)
		return string(v), err
	}},
	{"Quantity64", "0xffffffffffffffff", regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]{0,15})$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewQuantity64(raw)
		return string(v), err
	}},
	{"Quantity256", "0x" + strings.Repeat("f", 64), regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]{0,63})$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewQuantity256(raw)
		return string(v), err
	}},
	{"Data", "0x" + strings.Repeat("ab", 64), regexp.MustCompile(`^0x([0-9a-f]{2})*$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewData(raw)
		return string(v), err
	}},
	{"Address", "0x" + strings.Repeat("aB", 20), regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewAddress(raw)
		return string(v), err
	}},
	{"StorageKey", "0x" + strings.Repeat("a", 64), regexp.MustCompile(`^0x[0-9a-f]{0,64}$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewStorageKey(raw)
		return string(v), err
	}},
	{"HexByte", "0x04", regexp.MustCompile(`^0x[0-9a-f]{1,2}$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewHexByte(raw)
		return string(v), err
	}},
	{"DecimalString", "123456789012345678901234567890", regexp.MustCompile(`^(0|[1-9][0-9]*)$`), func(raw json.RawMessage) (string, error) {
		v, err := ethapi.NewDecimalString(raw)
		return string(v), err
	}},
}

func (constructor strictScalarConstructor) reference(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.Trim(raw, " \t\r\n"), []byte("null")) {
		return "", fmt.Errorf("%s: expected a non-null value", constructor.name)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("expected string: %w", err)
	}
	if !constructor.pattern.MatchString(text) {
		return "", fmt.Errorf("%s: invalid input spelling %q", constructor.name, text)
	}
	switch constructor.name {
	case "Address":
		text = strings.ToLower(text)
	case "HexByte":
		if len(text) == 4 && text[2] == '0' {
			text = "0x" + text[3:]
		}
	}
	return text, nil
}

func assertStrictScalarMatchesReference(t *testing.T, constructor strictScalarConstructor, raw json.RawMessage) {
	t.Helper()
	want, wantErr := constructor.reference(raw)
	got, gotErr := constructor.parse(raw)
	if got != want || !reflect.DeepEqual(gotErr, wantErr) {
		t.Fatalf("%s(%q) = %q, %T %v; reference = %q, %T %v", constructor.name, raw, got, gotErr, gotErr, want, wantErr, wantErr)
	}
}

func TestStrictScalarConstructorsMatchFrozenRegex(t *testing.T) {
	for _, constructor := range strictScalarConstructors {
		t.Run(constructor.name, func(t *testing.T) {
			for _, raw := range strictScalarContractInputs(constructor) {
				assertStrictScalarMatchesReference(t, constructor, raw)
			}
			for _, fixture := range strictScalarBenchmarkCases(constructor) {
				assertStrictScalarMatchesReference(t, constructor, fixture.raw)
			}
			// Exercise every byte at the prefix, first digit and final digit.
			// json.Marshal also makes control and invalid UTF-8 behavior explicit.
			for _, index := range []int{0, 1, 2, len(constructor.sample) - 1} {
				for c := 0; c <= 255; c++ {
					text := []byte(constructor.sample)
					text[index] = byte(c)
					raw, err := json.Marshal(string(text))
					if err != nil {
						t.Fatal(err)
					}
					assertStrictScalarMatchesReference(t, constructor, raw)
				}
			}
		})
	}
}

func FuzzStrictScalarConstructorsMatchFrozenRegex(f *testing.F) {
	for _, constructor := range strictScalarConstructors {
		for _, raw := range strictScalarContractInputs(constructor) {
			f.Add([]byte(raw))
		}
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		for _, constructor := range strictScalarConstructors {
			assertStrictScalarMatchesReference(t, constructor, raw)
		}
	})
}

func strictScalarContractInputs(constructor strictScalarConstructor) []json.RawMessage {
	text := constructor.sample
	inputs := []string{
		``, ` `, `null`, " \t\r\nnull\r\n", `true`, `0`, `{}`, `[]`, `""`,
		`"0"`, `"00"`, `"01"`, `"+1"`, `"-1"`, `"0x"`, `"0x0"`, `"0x00"`, `"0x01"`,
		`"0x4A"`, `"0x04"`, `"0X4a"`, `"0xgg"`, `"０"`, `"0xａ"`,
		`"` + text + `"`, `"` + strings.ToUpper(text) + `"`,
		fmt.Sprintf(`"\u%04x%s"`, text[0], text[1:]),
		" \t\r\n\"" + text + "\" \r\n\t",
		`"` + text + `\n"`, `"` + text + `\u0000"`,
		`"` + text + `" null`, `"` + text + `" ?`,
		`"` + text + `\ud800"`, `"` + text + "\xff\"",
		"\u00a0\"" + text + "\"", "\"" + text + "\"\u00a0",
		`"` + text + `\q"`, `"` + text,
	}
	for _, digits := range []int{0, 1, 2, 3, 15, 16, 17, 39, 40, 41, 63, 64, 65, 256, 4096} {
		inputs = append(inputs, `"0x`+strings.Repeat("a", digits)+`"`, `"0x`+strings.Repeat("0", digits)+`"`, `"`+strings.Repeat("1", digits)+`"`)
	}
	result := make([]json.RawMessage, len(inputs))
	for i, raw := range inputs {
		result[i] = json.RawMessage(raw)
	}
	return result
}
