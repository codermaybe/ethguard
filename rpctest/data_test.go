package rpctest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFailWithDataPreservesDataPresence(t *testing.T) {
	node := NewNode(1)
	defer node.Close()
	cases := []struct {
		name    string
		present bool
		data    string
		want    string
	}{
		{name: "missing", present: false, want: ""},
		{name: "null", present: true, data: "null", want: `"data":null`},
		{name: "empty", present: true, data: `"0x"`, want: `"data":"0x"`},
		{name: "hex", present: true, data: `"0xdeadbeef"`, want: `"data":"0xdeadbeef"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node.FailWithData("eth_call", 3, "execution reverted", json.RawMessage(tc.data), tc.present)
			body := postJSON(t, node.URL(), `{"jsonrpc":"2.0","id":1,"method":"eth_call","params":[]}`)
			if tc.want == "" {
				if strings.Contains(body, `"data"`) {
					t.Fatalf("missing data was written: %s", body)
				}
				return
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("body = %s, want %s", body, tc.want)
			}
		})
	}
}

func TestFailOmitsDataField(t *testing.T) {
	node := NewNode(1)
	defer node.Close()
	node.Fail("eth_call", 3, "execution reverted")
	body := postJSON(t, node.URL(), `{"jsonrpc":"2.0","id":7,"method":"eth_call","params":[]}`)
	if strings.Contains(body, `"data"`) {
		t.Fatalf("Fail wrote data: %s", body)
	}
	if !strings.Contains(body, `"id":7`) || !strings.Contains(body, `"code":3`) {
		t.Fatalf("body = %s", body)
	}
}

func postJSON(t *testing.T, url, payload string) string {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
