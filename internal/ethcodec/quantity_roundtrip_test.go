package ethcodec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"testing"
)

var frozenQuantityPattern = regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]*)$`)

// These references freeze the complete former quantity route rather than
// calling the optimized predicates or compatibility helpers under test.
func frozenNormalizeWireQuantity(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("hex quantity: expected string: %w", err)
	}
	if len(text) >= 2 && strings.EqualFold(text[:2], "0x") {
		text = strings.ToLower(text)
	}
	if !frozenQuantityPattern.MatchString(text) {
		return "", fmt.Errorf("hex quantity: invalid canonical quantity %q", text)
	}
	number, _ := new(big.Int).SetString(text[2:], 16)
	return number.String(), nil
}

func frozenCanonicalQuantity(raw json.RawMessage) (string, error) {
	decimal, err := frozenNormalizeWireQuantity(raw)
	if err != nil {
		return "", err
	}
	number, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		return "", fmt.Errorf("invalid quantity %q", decimal)
	}
	return "0x" + number.Text(16), nil
}

func frozenEncodeQuantity(raw json.RawMessage) (json.RawMessage, error) {
	decimal, err := frozenNormalizeWireQuantity(raw)
	if err != nil {
		return nil, err
	}
	number, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		return nil, fmt.Errorf("hex quantity: invalid quantity %q", decimal)
	}
	return json.Marshal("0x" + number.Text(16))
}

func quantityErrorSignature(err error) string {
	if err == nil {
		return ""
	}
	var signature strings.Builder
	signature.WriteString(err.Error())
	for current := err; current != nil; current = errors.Unwrap(current) {
		fmt.Fprintf(&signature, "|%T", current)
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		fmt.Fprintf(&signature, "|offset=%d", syntax.Offset)
	}
	var shape *json.UnmarshalTypeError
	if errors.As(err, &shape) {
		fmt.Fprintf(&signature, "|value=%s,type=%v,offset=%d,struct=%s,field=%s", shape.Value, shape.Type, shape.Offset, shape.Struct, shape.Field)
	}
	return signature.String()
}

func checkQuantityFrozenRoundTrip(t testing.TB, raw json.RawMessage) {
	t.Helper()
	for _, pair := range []struct {
		name          string
		got, expected func(json.RawMessage) (string, error)
	}{
		{"canonical", CanonicalQuantity, frozenCanonicalQuantity},
		{"normalize", NormalizeWireQuantity, frozenNormalizeWireQuantity},
	} {
		got, gotErr := pair.got(raw)
		want, wantErr := pair.expected(raw)
		if got != want || quantityErrorSignature(gotErr) != quantityErrorSignature(wantErr) {
			t.Fatalf("%s(%q) = %q / %v; frozen = %q / %v", pair.name, raw, got, gotErr, want, wantErr)
		}
	}
	got, gotErr := EncodeQuantity(raw)
	want, wantErr := frozenEncodeQuantity(raw)
	if !bytes.Equal(got, want) || (got == nil) != (want == nil) || quantityErrorSignature(gotErr) != quantityErrorSignature(wantErr) {
		t.Fatalf("encode(%q) = %q / %v; frozen = %q / %v", raw, got, gotErr, want, wantErr)
	}
}

func quantityRoundTripSeeds() [][]byte {
	seeds := [][]byte{
		nil, []byte(`null`), []byte(`true`), []byte(`1`), []byte(`[]`), []byte(`{}`), []byte(`""`),
		[]byte(`"0x"`), []byte(`"0x0"`), []byte(`"0x00"`), []byte(`"0x01"`), []byte(`"0XAbC"`),
		[]byte(`"\u0030\u0078\u0061"`), []byte(`"0x\u0041"`), []byte(`"0x1" null`), []byte(`"0x\uD800"`),
		[]byte{'"', '0', 'x', 0xff, '"'}, []byte("\t\n\r \"0xA\" \t\r\n"), []byte("\"0x1\n\""),
		[]byte("\"0x1\x00\""), []byte(`"0x-1"`), []byte(`"+0x1"`), []byte(`"0x１"`),
	}
	for _, size := range []int{1, 2, 16, 17, 64, 65, 256, 4096} {
		for _, text := range []string{"0x" + strings.Repeat("a", size), "0X" + strings.Repeat("A", size), "0x" + strings.Repeat("a", size-1) + "g"} {
			raw, _ := json.Marshal(text)
			seeds = append(seeds, raw)
		}
	}
	return seeds
}

func TestQuantityMatchesFrozenRoundTrip(t *testing.T) {
	for _, raw := range quantityRoundTripSeeds() {
		checkQuantityFrozenRoundTrip(t, raw)
	}
	for value := 0; value <= 255; value++ {
		text := "0x1" + string([]byte{byte(value)})
		raw, _ := json.Marshal(text)
		checkQuantityFrozenRoundTrip(t, raw)
		checkQuantityFrozenRoundTrip(t, []byte{'"', '0', 'x', '1', byte(value), '"'})
	}
}

func FuzzQuantityMatchesFrozenRoundTrip(f *testing.F) {
	for _, seed := range quantityRoundTripSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 8192 {
			return
		}
		checkQuantityFrozenRoundTrip(t, raw)
	})
}
