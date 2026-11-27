package rpcwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// frozenEncodeRequest is the complete pre-assembly implementation. It keeps
// the outer RawMessage marshal as an independent byte and error oracle.
func frozenEncodeRequest(id json.RawMessage, method string, params []any) ([]byte, error) {
	if _, ok := parseID(id); !ok {
		return nil, errors.New("rpcwire: request id must be a JSON number or string")
	}
	if method == "" {
		return nil, errors.New("rpcwire: request method must not be empty")
	}
	var paramsJSON json.RawMessage
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("rpcwire: encode params: %w", err)
		}
		paramsJSON = raw
	}
	msg := struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{Version, append(json.RawMessage(nil), id...), method, paramsJSON}
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("rpcwire: encode request: %w", err)
	}
	return out, nil
}

func checkRequestAssembly(tb testing.TB, id json.RawMessage, method string, params []any) {
	tb.Helper()
	want, wantErr := frozenEncodeRequest(id, method, params)
	got, gotErr := EncodeRequest(id, method, params)
	if !bytes.Equal(got, want) || !reflect.DeepEqual(gotErr, wantErr) {
		tb.Fatalf("request assembly differs:\n got %s, %v\nwant %s, %v", got, gotErr, want, wantErr)
	}
}

func TestRequestAssemblyMatchesFrozenEncoding(t *testing.T) {
	params := [][]any{
		nil, {}, {nil}, {"<>&\u2028\u2029", "\xff"},
		{json.RawMessage(` { "a": "<>&", "large":12345678901234567890 } `)},
		{json.RawMessage(`"\u003c\u2028"`), []any{true, nil, "x"}},
		{json.RawMessage(`1 2`)}, {make(chan int)},
	}
	for _, id := range []string{`1`, ` 2 `, `1e+100`, `"quoted\"<>&"`, `1 2`, `"x" null`, `null`, `{}`, ``} {
		for _, method := range []string{"eth_sendRawTransaction", "method\"<>&\u2028\xff", ""} {
			for _, values := range params {
				checkRequestAssembly(t, json.RawMessage(id), method, values)
			}
		}
	}
}

type requestParamCallback func() ([]byte, error)

func (f requestParamCallback) MarshalJSON() ([]byte, error) { return f() }

func TestRequestAssemblyPreservesMarshalOrderAndCalls(t *testing.T) {
	for _, encode := range []struct {
		name string
		call func(json.RawMessage, string, []any) ([]byte, error)
	}{{"production", EncodeRequest}, {"frozen", frozenEncodeRequest}} {
		t.Run(encode.name, func(t *testing.T) {
			id := json.RawMessage(`1`)
			calls := 0
			value := requestParamCallback(func() ([]byte, error) {
				calls++
				id[0] = '2'
				return []byte(` { "value": "<>&" } `), nil
			})
			got, err := encode.call(id, "method", []any{value})
			want := `{"jsonrpc":"2.0","id":2,"method":"method","params":[{"value":"\u003c\u003e\u0026"}]}`
			if err != nil || string(got) != want || calls != 1 {
				t.Fatalf("wire=%s error=%v calls=%d", got, err, calls)
			}
			failure := errors.New("parameter failed")
			badParam := requestParamCallback(func() ([]byte, error) {
				calls++
				return nil, failure
			})
			for _, tc := range []struct {
				id, method, prefix string
				wantCalls          int
			}{
				{"null", "", "rpcwire: request id", 0},
				{"1 2", "", "rpcwire: request method", 0},
				{"1 2", "method", "rpcwire: encode params", 1},
			} {
				calls = 0
				_, err := encode.call(json.RawMessage(tc.id), tc.method, []any{badParam})
				if err == nil || !strings.HasPrefix(err.Error(), tc.prefix) || calls != tc.wantCalls {
					t.Fatalf("id=%q method=%q: error=%v calls=%d", tc.id, tc.method, err, calls)
				}
			}
		})
	}
}

func FuzzRequestAssemblyMatchesFrozenEncoding(f *testing.F) {
	for _, id := range []string{`1`, `"<>&"`, `1 2`, `null`} {
		for _, param := range []string{`null`, ` [ "<>&", {"n":12345678901234567890} ] `, `"\u2028"`, `1 2`} {
			f.Add([]byte(id), "method", []byte(param))
		}
	}
	f.Fuzz(func(t *testing.T, id []byte, method string, param []byte) {
		if len(id)+len(method)+len(param) > 1<<20 {
			t.Skip()
		}
		checkRequestAssembly(t, json.RawMessage(id), method, []any{json.RawMessage(param)})
	})
}

func BenchmarkRequestAssembly(b *testing.B) {
	for _, size := range []int{64, 512, 131072} {
		params := frozenParamsElements(b, frozenParamsJSON(size))
		checkRequestAssembly(b, json.RawMessage(`1`), "eth_sendRawTransaction", params)
		for _, encode := range []struct {
			name string
			call func(json.RawMessage, string, []any) ([]byte, error)
		}{{"production", EncodeRequest}, {"frozen", frozenEncodeRequest}} {
			b.Run(fmt.Sprintf("bytes-%d/%s", size, encode.name), func(b *testing.B) {
				ids := NewIDGen()
				b.ReportAllocs()
				for range b.N {
					if _, err := encode.call(ids.Next(), "eth_sendRawTransaction", params); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
