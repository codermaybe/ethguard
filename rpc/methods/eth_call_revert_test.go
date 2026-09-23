package methods

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ethguard/rpc"
)

func TestNormalizeCallOutcomeSeparatesRevertDataStates(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"missing data", `{"code":3,"message":"execution reverted"}`, "reverted|missing"},
		{"json null data", `{"code":3,"message":"execution reverted","data":null}`, "reverted|null"},
		{"empty bytes", `{"code":3,"message":"execution reverted","data":"0x"}`, "reverted|empty"},
		{"empty bytes uppercase prefix", `{"code":-32000,"message":"execution reverted","data":"0X"}`, "reverted|empty"},
		{"non-empty hex", `{"code":3,"message":"execution reverted","data":"0xDEAD"}`, "reverted|hex:0xdead"},
		{"one non-zero byte is not empty", `{"code":3,"message":"execution reverted","data":"0x00"}`, "reverted|hex:0x00"},
		{"empty string is not empty bytes", `{"code":3,"message":"execution reverted","data":""}`, `reverted|raw:""`},
		{"structured data stays raw", `{"code":3,"message":"execution reverted","data":{"reason":"nope","code":1}}`, `reverted|raw:{"code":1,"reason":"nope"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCallOutcome(json.RawMessage(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeCallOutcomeSameWordingDifferentDataDisagrees(t *testing.T) {
	const message = "execution reverted"
	left, err := normalizeCallOutcome(json.RawMessage(`{"code":3,"message":"` + message + `","data":"0xdead"}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := normalizeCallOutcome(json.RawMessage(`{"code":-32000,"message":"` + message + `","data":"0xbeef"}`))
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatalf("same wording hid different data: %s", left)
	}
	sameData, err := normalizeCallOutcome(json.RawMessage(`{"code":3,"message":"execution reverted: other wording","data":"0xDEAD"}`))
	if err != nil {
		t.Fatal(err)
	}
	if sameData != left {
		t.Fatalf("wording or vendor code changed the data key: %s vs %s", sameData, left)
	}
}

func TestCallRevertDataConflictIsNotSwallowed(t *testing.T) {
	policy := CallPolicy(func(ctx context.Context) (string, error) {
		return "", errors.New("anchor should not run")
	})
	obs := []rpc.Observation{
		rpcErrorObs("a", -32000, "execution reverted", `"0xdead"`),
		rpcErrorObs("b", 3, "execution reverted", `"0xbeef"`),
	}
	decision, err := policy.Resolve(obs)
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if errors.Is(err, rpc.ErrNoValidResponse) || errors.Is(err, rpc.ErrMalformedResponse) || errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("data conflict swallowed as %v", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %v, want Unknown", decision.Status)
	}
}

func TestCallProviderErrorDoesNotEnterRevertKey(t *testing.T) {
	policy := CallPolicy(nil)
	obs := []rpc.Observation{
		rpcErrorObs("a", -32002, "request timed out", ""),
		rpcErrorObs("b", -32003, "response too large", ""),
		rpcErrorObs("c", -32603, "panic", ""),
		{Source: rpc.SourceRef{Name: "d", ID: "d"}, Err: errors.New("429 too many requests")},
	}
	decision, err := policy.Resolve(obs)
	if !errors.Is(err, rpc.ErrNoValidResponse) {
		t.Fatalf("err = %v, want ErrNoValidResponse", err)
	}
	if errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
		t.Fatalf("provider errors entered adjudication: status %v err %v", decision.Status, err)
	}
}

func TestCallRPCErrorRawPreservesDataAbsence(t *testing.T) {
	missing := rpc.RPCError{Code: 3, Message: "execution reverted"}.Raw()
	nullData := rpc.RPCError{Code: 3, Message: "execution reverted", Data: json.RawMessage("null")}.Raw()
	empty := rpc.RPCError{Code: 3, Message: "execution reverted", Data: json.RawMessage(`"0x"`)}.Raw()
	gotMissing, err := normalizeCallOutcome(missing)
	if err != nil || gotMissing != "reverted|missing" {
		t.Fatalf("missing raw %s -> %s, %v", missing, gotMissing, err)
	}
	gotNull, err := normalizeCallOutcome(nullData)
	if err != nil || gotNull != "reverted|null" {
		t.Fatalf("null raw %s -> %s, %v", nullData, gotNull, err)
	}
	gotEmpty, err := normalizeCallOutcome(empty)
	if err != nil || gotEmpty != "reverted|empty" {
		t.Fatalf("empty raw %s -> %s, %v", empty, gotEmpty, err)
	}
}

func rpcErrorObs(id string, code int, message, data string) rpc.Observation {
	errBody := &rpc.RPCError{Code: code, Message: message}
	if data != "" {
		errBody.Data = json.RawMessage(data)
	}
	return rpc.Observation{
		Source:   rpc.SourceRef{Name: id, ID: id},
		Err:      errors.New(message),
		RPCError: errBody,
	}
}

func TestRevertSamplesUseDistinctDataKeys(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "rpcsample", "testdata", "revert-data")
	keys := map[string]map[string]bool{}
	indexRaw, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Records []struct {
			RequestID string `json:"request_id"`
			File      string `json:"file"`
		} `json:"records"`
	}
	if err := json.Unmarshal(indexRaw, &index); err != nil {
		t.Fatal(err)
	}
	for _, entry := range index.Records {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.File)))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Response struct {
				Body string `json:"body"`
			} `json:"response"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(doc.Response.Body), &body); err != nil || len(body.Error) == 0 {
			t.Fatalf("%s: %v", entry.File, err)
		}
		key, err := normalizeCallOutcome(body.Error)
		if err != nil {
			t.Fatalf("%s: %v", entry.File, err)
		}
		group := entry.RequestID
		if strings.HasPrefix(group, "revert-empty") {
			group = "revert-empty"
		}
		if keys[group] == nil {
			keys[group] = map[string]bool{}
		}
		keys[group][key] = true
	}
	if len(keys["revert-empty"]) < 2 {
		t.Fatalf("empty-revert sample keys = %v, want missing and empty", keys["revert-empty"])
	}
	hexKeys := keys["revert-hex"]
	if len(hexKeys) != 1 {
		t.Fatalf("hex sample keys = %v", hexKeys)
	}
	for key := range hexKeys {
		if _, dup := keys["revert-empty"][key]; dup {
			t.Fatalf("non-empty hex collapsed into an empty-revert key %s", key)
		}
	}
	for key := range keys["revert-error-string"] {
		if _, dup := hexKeys[key]; dup {
			t.Fatalf("Error(string) data collapsed into the selector key %s", key)
		}
	}
}
