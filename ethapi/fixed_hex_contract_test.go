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

type fixedHexConstructor struct {
	name    string
	digits  int
	pattern *regexp.Regexp
	parse   func(json.RawMessage) (string, error)
}

// Freeze the three native patterns and constructor semantics independently
// of the generator and any production ASCII predicate. Other native patterns,
// arbitrary schema constraints, and representation policies are out of scope.
var fixedHexConstructors = []fixedHexConstructor{
	{name: "Hash", digits: 64, pattern: regexp.MustCompile(`^0x[0-9a-f]{64}$`), parse: func(raw json.RawMessage) (string, error) {
		value, err := ethapi.NewHash(raw)
		return string(value), err
	}},
	{name: "Bloom", digits: 512, pattern: regexp.MustCompile(`^0x[0-9a-f]{512}$`), parse: func(raw json.RawMessage) (string, error) {
		value, err := ethapi.NewBloom(raw)
		return string(value), err
	}},
	{name: "BlockNonce", digits: 16, pattern: regexp.MustCompile(`^0x[0-9a-f]{16}$`), parse: func(raw json.RawMessage) (string, error) {
		value, err := ethapi.NewBlockNonce(raw)
		return string(value), err
	}},
}

// This is the pre-optimization generated constructor: null recognition, the
// standard string decoder, and the frozen regular expression own the oracle.
func (constructor fixedHexConstructor) reference(raw json.RawMessage) (string, error) {
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
	return text, nil
}

func assertFixedHexConstructorMatchesReference(t *testing.T, constructor fixedHexConstructor, raw json.RawMessage) {
	t.Helper()
	want, wantErr := constructor.reference(raw)
	got, gotErr := constructor.parse(raw)
	if got != want || !reflect.DeepEqual(gotErr, wantErr) {
		t.Fatalf("%s(%q) = %q, %T %v; reference = %q, %T %v", constructor.name, raw, got, gotErr, gotErr, want, wantErr, wantErr)
	}
}

func TestFixedHexConstructorsMatchFrozenRegex(t *testing.T) {
	for _, constructor := range fixedHexConstructors {
		t.Run(constructor.name, func(t *testing.T) {
			for _, fixture := range fixedHexBenchmarkCases(constructor) {
				assertFixedHexConstructorMatchesReference(t, constructor, fixture.raw)
				if _, err := constructor.parse(fixture.raw); (err == nil) != fixture.valid {
					t.Fatalf("%s valid = %v, want %v: %v", fixture.name, err == nil, fixture.valid, err)
				}
			}
			for _, raw := range fixedHexContractInputs(constructor) {
				assertFixedHexConstructorMatchesReference(t, constructor, raw)
			}
		})
	}
}

func TestFixedHexConstructorsRejectEveryNonHexByte(t *testing.T) {
	for _, constructor := range fixedHexConstructors {
		t.Run(constructor.name, func(t *testing.T) {
			for _, index := range []int{2, constructor.digits/2 + 2, constructor.digits + 1} {
				for c := 0; c <= 255; c++ {
					text := []byte("0x" + strings.Repeat("a", constructor.digits))
					text[index] = byte(c)
					raw, err := json.Marshal(string(text))
					if err != nil {
						t.Fatal(err)
					}
					assertFixedHexConstructorMatchesReference(t, constructor, raw)
					_, err = constructor.parse(raw)
					valid := c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
					if (err == nil) != valid {
						t.Fatalf("digit %d byte %#x valid = %v, want %v: %v", index, c, err == nil, valid, err)
					}
				}
			}
		})
	}
}

func FuzzFixedHexConstructorsMatchFrozenRegex(f *testing.F) {
	for _, constructor := range fixedHexConstructors {
		for _, raw := range fixedHexContractInputs(constructor) {
			f.Add([]byte(raw))
		}
		for _, fixture := range fixedHexBenchmarkCases(constructor) {
			if len(fixture.raw) <= 64<<10 {
				f.Add([]byte(fixture.raw))
			}
		}
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		for _, constructor := range fixedHexConstructors {
			assertFixedHexConstructorMatchesReference(t, constructor, raw)
		}
	})
}

func fixedHexContractInputs(constructor fixedHexConstructor) []json.RawMessage {
	digits := strings.Repeat("a", constructor.digits)
	inputs := []string{
		``, ` `, `null`, " \t\r\nnull\r\n", `true`, `0`, `{}`, `[]`,
		`"0x` + digits + `"`, `"0x` + strings.Repeat("0", constructor.digits) + `"`,
		`"0X` + digits + `"`, `"0x` + strings.Repeat("A", constructor.digits) + `"`,
		`"0x` + digits[:len(digits)-1] + `"`, `"0x` + digits + `a"`,
		`"\u0030\u0078` + digits + `"`, `"0x` + strings.Repeat(`\u0061`, constructor.digits) + `"`,
		" \t\r\n\"0x" + digits + "\" \r\n\t",
		`"0x` + digits + `\n"`, `"0x` + digits + `\u0000"`,
		`"0x` + digits + `" null`, `"0x` + digits + `" ?`,
		`"0x` + digits[:len(digits)-1] + `\ud800"`,
		`"0x` + digits[:len(digits)-1] + "\xff\"",
		`"0x` + digits[:len(digits)-1] + `ａ"`,
		"\u00a0\"0x" + digits + "\"", "\"0x" + digits + "\"\u00a0",
		`"0x` + digits + `\q"`, `"0x` + digits,
	}
	result := make([]json.RawMessage, len(inputs))
	for i, raw := range inputs {
		result[i] = json.RawMessage(raw)
	}
	return result
}
