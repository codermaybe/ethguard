package block

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	objectheader "github.com/codermaybe/ethguard/internal/ethobject/header"
)

// frozenBlockEncode retains the former ordering and repeated header work.
// The shared body helpers have not changed their projection or schema rules.
func frozenBlockEncode(raw json.RawMessage) (json.RawMessage, error) {
	if ethcodec.IsJSONNull(raw) {
		return json.RawMessage("null"), nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil || source == nil {
		return nil, fmt.Errorf("block encode: expected object")
	}
	if !hasBlockHead(source) {
		return nil, fmt.Errorf("block head: missing number or hash")
	}
	fields, err := projectHeader(raw)
	if err != nil {
		return nil, err
	}
	reference := objectheader.ReferenceOnly(source)
	for _, name := range []string{"size", "transactions", "uncles", "withdrawals"} {
		if _, present := source[name]; present {
			reference = false
		}
	}
	if reference {
		return encodeHeaderOnly(source)
	}
	if _, err := objectheader.Decode(raw); err != nil {
		return nil, fmt.Errorf("block encode: %w", err)
	}
	if _, txs := source["transactions"]; !txs {
		if _, uncles := source["uncles"]; !uncles {
			return objectheader.Normalize(raw)
		}
	}
	if err := projectBody(raw, fields); err != nil {
		return nil, err
	}
	out, err := parseBlock(fields)
	if err != nil {
		return nil, fmt.Errorf("block encode: %w", err)
	}
	return ethcodec.MarshalNoEscape(out)
}

func TestBlockHeaderReusePreservesPublicEncoding(t *testing.T) {
	raw, _ := completeBenchmarkBlock(t, 1)
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"transactions", "uncles", "size"} {
		delete(source, field)
	}
	header := marshalBlockBenchmarkValue(t, source)
	cases := []json.RawMessage{
		raw, header,
		json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`)),
		json.RawMessage(strings.ReplaceAll(string(header), `"0x`, `"0X`)),
		json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{`),
		json.RawMessage(`{"vendor":1,"vendor":2}`),
		json.RawMessage(`{"number":"0x1","hash":"0x11","vendor":"<>&"}`),
		json.RawMessage(`{"number":"0x1","hash":"0x11","vendor":1,"vendor":2}`),
		json.RawMessage(`{"number":"0x1","hash":"bad","transactions":false}`),
	}
	for _, field := range []string{"hash", "number", "difficulty", "baseFeePerGas", "slotNumber", "size", "transactions", "uncles"} {
		for _, value := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`"bad"`), json.RawMessage(`{}`)} {
			var changed map[string]json.RawMessage
			_ = json.Unmarshal(raw, &changed)
			if value == nil {
				delete(changed, field)
			} else {
				changed[field] = value
			}
			cases = append(cases, marshalBlockBenchmarkValue(t, changed))
		}
	}
	for i, input := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			before := bytes.Clone(input)
			want, wantErr := frozenBlockEncode(input)
			got, gotErr := Encode(input)
			if !bytes.Equal(got, want) || !reflect.DeepEqual(gotErr, wantErr) {
				t.Fatalf("got %s, %v; want %s, %v", got, gotErr, want, wantErr)
			}
			if !bytes.Equal(input, before) {
				t.Fatal("encoder mutated input")
			}
		})
	}
}

func TestBlockAdmissionReusesEquivalentHeaderValue(t *testing.T) {
	for _, count := range []int{0, 1, 100} {
		raw, req := completeBenchmarkBlock(t, count)
		for _, input := range []json.RawMessage{
			raw,
			json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`)),
			json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"0X`)),
			json.RawMessage(string(raw[:len(raw)-1]) + `,"vendor":{"text":"<>&"}}`),
		} {
			before := bytes.Clone(input)
			want, err := frozenBlockEncode(input)
			if err != nil {
				t.Fatal(err)
			}
			full, err := AdmitBlock(t.Context(), req, input, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(full.canonical, want) || !bytes.Equal(before, input) {
				t.Fatal("header reuse changed the canonical block or source bytes")
			}
		}
	}
}

func TestBlockAdmissionKeepsTransactionAliasViewAndErrorOrder(t *testing.T) {
	raw, req := completeBenchmarkBlock(t, 0)
	for _, tc := range []struct {
		name, raw, wantError string
	}{
		{"alias before exact", `{"Transactions":[null],` + string(raw[1:]), ""},
		{"alias after exact", string(raw[:len(raw)-1]) + `,"Transactions":[null]}`, "transaction 0 is hash-only"},
		{"null alias", string(raw[:len(raw)-1]) + `,"Transactions":null}`, ""},
		{"malformed alias", string(raw[:len(raw)-1]) + `,"Transactions":{}}`, "block transactions"},
		{"missing head before duplicate", `{"vendor":1,"vendor":2}`, "block header: header JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full, err := AdmitBlock(t.Context(), req, json.RawMessage(tc.raw), true, nil)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %q", err, tc.wantError)
				}
				return
			}
			want, wantErr := frozenBlockEncode(json.RawMessage(tc.raw))
			if err != nil || wantErr != nil || !bytes.Equal(full.canonical, want) {
				t.Fatalf("admission=%v, encoding=%v", err, wantErr)
			}
		})
	}
	for _, field := range []string{"hash", "size"} {
		var source map[string]json.RawMessage
		_ = json.Unmarshal(raw, &source)
		source[field] = json.RawMessage(`"bad"`)
		source["Transactions"] = json.RawMessage(`{}`)
		input := marshalBlockBenchmarkValue(t, source)
		_, err := AdmitBlock(t.Context(), req, input, true, nil)
		stage := "block shape"
		if field == "hash" {
			stage = "block header"
		}
		if err == nil || !strings.Contains(err.Error(), stage) {
			t.Fatalf("%s error=%v, want %s before malformed alias", field, err, stage)
		}
	}
}
