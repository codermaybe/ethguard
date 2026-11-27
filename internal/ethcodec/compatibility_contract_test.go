package ethcodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

// Freeze the former standard-library decode/transform/encode route without
// using the decoder or text helpers being optimized.
func frozenScalarCompatibility(raw json.RawMessage, kind string) (json.RawMessage, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		if kind == "decimal" {
			return nil, fmt.Errorf("decimal string: expected string: %w", err)
		}
		return nil, fmt.Errorf("expected string: %w", err)
	}
	switch kind {
	case "wire":
		if len(text) >= 2 && strings.EqualFold(text[:2], "0x") {
			text = strings.ToLower(text)
		}
	case "lower", "peer":
		if len(text) < 2 || text[:2] != "0x" {
			return nil, fmt.Errorf("expected lowercase 0x prefix")
		}
		text = strings.ToLower(text)
		if kind == "peer" && len(text) > 2 {
			digits := strings.TrimLeft(text[2:], "0")
			if digits == "" {
				digits = "0"
			}
			text = "0x" + digits
		}
	case "decimal":
		number, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return nil, fmt.Errorf("decimal string: invalid number %q", text)
		}
		text = number.String()
	default:
		panic("unknown frozen compatibility")
	}
	return json.Marshal(text)
}

func checkScalarCompatibility(t testing.TB, raw json.RawMessage) {
	t.Helper()
	for _, fixture := range []struct {
		kind string
		fold func(json.RawMessage) (json.RawMessage, error)
	}{
		{"wire", FoldWireHexCase},
		{"lower", FoldLowerPrefixHexCase},
		{"peer", FoldPeerQuantity},
		{"decimal", FoldLegacyDecimal},
	} {
		original := bytes.Clone(raw)
		want, wantErr := frozenScalarCompatibility(raw, fixture.kind)
		got, gotErr := fixture.fold(raw)
		if !bytes.Equal(got, want) || (got == nil) != (want == nil) || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("%s(%q) = %q, %T %v; frozen = %q, %T %v", fixture.kind, raw, got, gotErr, gotErr, want, wantErr, wantErr)
		}
		if !bytes.Equal(raw, original) {
			t.Fatalf("%s modified provider input", fixture.kind)
		}
	}
}

func scalarCompatibilitySeeds() [][]byte {
	seeds := quantityRoundTripSeeds()
	for _, text := range []string{"0x000A", "0x000", "+0001", "-0", "-1", "0001", "0x<&>", "<&>", "0x\u2028\u2029", "0xK", "0xİ"} {
		raw, _ := json.Marshal(text)
		seeds = append(seeds, raw)
	}
	return seeds
}

func TestScalarCompatibilityMatchesFrozenRoute(t *testing.T) {
	for _, raw := range scalarCompatibilitySeeds() {
		checkScalarCompatibility(t, raw)
	}
	for c := 0; c <= 255; c++ {
		checkScalarCompatibility(t, []byte{'"', '0', 'x', byte(c), '"'})
	}
}

func FuzzScalarCompatibilityMatchesFrozenRoute(f *testing.F) {
	for _, raw := range scalarCompatibilitySeeds() {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 8192 {
			t.Skip()
		}
		checkScalarCompatibility(t, raw)
	})
}
