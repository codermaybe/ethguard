package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
)

type frozenScalarRoute struct {
	method    string
	normalize func(json.RawMessage) (string, error)
	encode    func(json.RawMessage) (json.RawMessage, error)
}

// These references retain the former compatibility -> generated root ->
// re-encoding routes. Compatibility and constructors have independent frozen
// decoder/regex differential tests in their owning packages.
func frozenQuantityRoute(method string, parse func(json.RawMessage) (ethapi.Quantity, error), compatibility scalarCompatibility) frozenScalarRoute {
	prepare := func(raw json.RawMessage) (ethapi.Quantity, error) {
		candidate, err := compatibility(raw)
		if err != nil {
			return "", fmt.Errorf("hex quantity: %w", err)
		}
		value, err := parse(candidate)
		if err != nil {
			return "", fmt.Errorf("hex quantity: %w", err)
		}
		return value, nil
	}
	return frozenScalarRoute{method,
		func(raw json.RawMessage) (string, error) {
			value, err := prepare(raw)
			if err != nil {
				return "", err
			}
			return ethcodec.QuantityDecimal(string(value))
		},
		func(raw json.RawMessage) (json.RawMessage, error) {
			value, err := prepare(raw)
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		},
	}
}

func frozenNullableQuantityRoute(method string, parse func(json.RawMessage) (*ethapi.Quantity, error), allowNull bool) frozenScalarRoute {
	normalize := func(raw json.RawMessage) (string, error) {
		candidate := raw
		if !ethcodec.IsJSONNull(raw) {
			var err error
			candidate, err = ethcodec.FoldWireHexCase(raw)
			if err != nil {
				return "", fmt.Errorf("hex quantity: %w", err)
			}
		}
		value, err := parse(candidate)
		if err != nil {
			return "", fmt.Errorf("hex quantity: %w", err)
		}
		if value == nil {
			if allowNull {
				return "null", nil
			}
			return "", fmt.Errorf("hex quantity: null is not admitted by this method")
		}
		return ethcodec.QuantityDecimal(string(*value))
	}
	return frozenScalarRoute{method, normalize, func(raw json.RawMessage) (json.RawMessage, error) {
		if _, err := normalize(raw); err != nil {
			return nil, err
		}
		if ethcodec.IsJSONNull(raw) {
			return json.RawMessage("null"), nil
		}
		return ethcodec.FoldWireHexCase(raw)
	}}
}

func frozenDataRoute(method string, parse func(json.RawMessage) (ethapi.Data, error), size int) frozenScalarRoute {
	kind := "hex data"
	if size > 0 {
		kind = "storage word"
	}
	normalize := func(raw json.RawMessage) (string, error) {
		candidate, err := ethcodec.FoldLowerPrefixHexCase(raw)
		if err != nil {
			return "", fmt.Errorf("%s: %w", kind, err)
		}
		value, err := parse(candidate)
		if err != nil {
			return "", fmt.Errorf("%s: %w", kind, err)
		}
		if size > 0 && len(value) != 2+2*size {
			return "", fmt.Errorf("storage word: expected %d-byte data", size)
		}
		return string(value), nil
	}
	return frozenScalarRoute{method, normalize, func(raw json.RawMessage) (json.RawMessage, error) {
		value, err := normalize(raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}}
}

func frozenStandardScalarRoutes() []frozenScalarRoute {
	return []frozenScalarRoute{
		frozenQuantityRoute(MethodChainID, ethapi.NewChainIdResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodBlockNumber, ethapi.NewBlockNumberResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodGetBalance, ethapi.NewGetBalanceResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodGetTransactionCount, ethapi.NewGetTransactionCountResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodEstimateGas, ethapi.NewEstimateGasResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodGasPrice, ethapi.NewGasPriceResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodMaxPriorityFeePerGas, ethapi.NewMaxPriorityFeePerGasResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodBlobBaseFee, ethapi.NewBlobBaseFeeResult, ethcodec.FoldWireHexCase),
		frozenQuantityRoute(MethodPeerCount, ethapi.NewPeerCountResult, ethcodec.FoldPeerQuantity),
		frozenNullableQuantityRoute(MethodGetBlockTransactionCountByHash, ethapi.NewGetBlockTransactionCountByHashResult, true),
		frozenNullableQuantityRoute(MethodGetBlockTransactionCountByNumber, ethapi.NewGetBlockTransactionCountByNumberResult, false),
		frozenDataRoute(MethodGetCode, ethapi.NewGetCodeResult, 0),
		frozenDataRoute(MethodCall, ethapi.NewCallResult, 0),
		frozenDataRoute(MethodGetStorageAt, ethapi.NewGetStorageAtResult, 32),
	}
}

func scalarRouteInputs() []json.RawMessage {
	inputs := []json.RawMessage{
		nil, {}, []byte(`null`), []byte(" \tnull\n"), []byte(`true`), []byte(`1`), []byte(`{}`), []byte(`[]`),
		[]byte(`""`), []byte(`"0x"`), []byte(`"0x0"`), []byte(`"0x00"`), []byte(`"0x01"`),
		[]byte(`"0XA"`), []byte(`"0xA"`), []byte(`"0x000A"`), []byte(`"0xgg"`), []byte(`"0x<&>"`),
		[]byte(`"\u0030x\u0041"`), []byte(" \t\"0xAB\"\r\n"), []byte(`"0x1" null`),
		[]byte(`"0x\ud800"`), []byte(`"0x\q"`), []byte(`"0x1`), []byte("\"0x1\n\""),
	}
	for _, digits := range []int{1, 2, 63, 64, 65, 4096} {
		inputs = append(inputs, json.RawMessage(`"0x`+strings.Repeat("A", digits)+`"`))
	}
	for c := 0; c <= 255; c++ {
		inputs = append(inputs, json.RawMessage{'"', '0', 'x', byte(c), '"'})
	}
	return inputs
}

func checkStandardScalarRoutes(t testing.TB, raw json.RawMessage) {
	t.Helper()
	specs := map[string]MethodSpec{}
	for _, spec := range Specs() {
		specs[spec.Method] = spec
	}
	for _, frozen := range frozenStandardScalarRoutes() {
		spec := specs[frozen.method]
		if frozen.method == MethodCall {
			// eth_call owns a separate success/revert classifier rather than
			// MethodSpec.Encode. Exercise the scalar branch it delegates to.
			spec.Encode = standardDataEncoding(ethapi.NewCallResult, 0)
			spec.Normalize = standardDataResult(ethapi.NewCallResult, 0)
		}
		original := bytes.Clone(raw)
		want, wantErr := frozen.encode(raw)
		got, gotErr := spec.Encode(raw)
		if !bytes.Equal(got, want) || (got == nil) != (want == nil) || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("%s.Encode(%q) = %q, %T %v; frozen = %q, %T %v", frozen.method, raw, got, gotErr, gotErr, want, wantErr, wantErr)
		}
		wantText, wantErr := frozen.normalize(raw)
		gotText, gotErr := spec.Normalize(raw)
		if gotText != wantText || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("%s.Normalize(%q) = %q, %T %v; frozen = %q, %T %v", frozen.method, raw, gotText, gotErr, gotErr, wantText, wantErr, wantErr)
		}
		if !bytes.Equal(raw, original) {
			t.Fatalf("%s modified provider input", frozen.method)
		}
	}
}

func TestStandardScalarRoutesMatchFrozenRoundTrips(t *testing.T) {
	for _, raw := range scalarRouteInputs() {
		checkStandardScalarRoutes(t, raw)
	}
}

func FuzzStandardScalarRoutesMatchFrozenRoundTrips(f *testing.F) {
	for _, raw := range scalarRouteInputs() {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 8192 {
			t.Skip()
		}
		checkStandardScalarRoutes(t, raw)
	})
}

func TestScalarEncodingRetainsMethodRootAndErrorOrder(t *testing.T) {
	rootErr := errors.New("method root rejected candidate")
	quantityRoot := func(raw json.RawMessage) (ethapi.Quantity, error) { return "", rootErr }
	nullableRoot := func(raw json.RawMessage) (*ethapi.Quantity, error) { return nil, rootErr }
	dataRoot := func(raw json.RawMessage) (ethapi.Data, error) { return "", rootErr }
	for _, fixture := range []struct {
		name   string
		encode func(json.RawMessage) (json.RawMessage, error)
	}{
		{"quantity", standardQuantityEncoding(quantityRoot, ethcodec.FoldWireHexCase)},
		{"peer", standardQuantityEncoding(quantityRoot, ethcodec.FoldPeerQuantity)},
		{"nullable", standardNullableQuantityEncoding(nullableRoot, false)},
		{"data", standardDataEncoding(dataRoot, 0)},
		{"storage", standardDataEncoding(dataRoot, 32)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// A root constraint must win over the storage length check, and
			// no already-encoded candidate can bypass the generated root.
			if value, err := fixture.encode(json.RawMessage(`"0xA"`)); value != nil || !errors.Is(err, rootErr) {
				t.Fatalf("root not enforced first: %s, %v", value, err)
			}
			if value, err := fixture.encode(json.RawMessage(`true`)); value != nil || err == nil || errors.Is(err, rootErr) {
				t.Fatalf("compatibility decode did not precede root: %s, %v", value, err)
			}
		})
	}
	if value, err := standardNullableQuantityEncoding(nullableRoot, false)(json.RawMessage(`null`)); value != nil || !errors.Is(err, rootErr) {
		t.Fatalf("method null root must precede the allowNull policy: %s, %v", value, err)
	}
}

func BenchmarkStandardScalarEncoding(b *testing.B) {
	for _, fixture := range []struct {
		name, method, raw string
	}{
		{"quantity/plain", MethodBlockNumber, `"0xabcdef"`},
		{"quantity/large", MethodBlockNumber, `"0x` + strings.Repeat("a", 4096) + `"`},
		{"quantity/escaped", MethodBlockNumber, `"\u0030xabcdef"`},
		{"quantity/uppercase", MethodBlockNumber, `"0XABCDEF"`},
		{"peer/leading-zeroes", MethodPeerCount, `"0x000ABC"`},
		{"nullable/value", MethodGetBlockTransactionCountByHash, `"0xabcdef"`},
		{"nullable/large", MethodGetBlockTransactionCountByHash, `"0x` + strings.Repeat("a", 4096) + `"`},
		{"nullable/null", MethodGetBlockTransactionCountByHash, `null`},
		{"data/small", MethodGetCode, `"0x00aabbcc"`},
		{"data/large", MethodGetCode, `"0x` + strings.Repeat("ab", 4096) + `"`},
		{"storage", MethodGetStorageAt, `"0x` + strings.Repeat("ab", 32) + `"`},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			var encode func(json.RawMessage) (json.RawMessage, error)
			for _, spec := range Specs() {
				if spec.Method == fixture.method {
					encode = spec.Encode
				}
			}
			raw := json.RawMessage(fixture.raw)
			if _, err := encode(raw); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := encode(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
