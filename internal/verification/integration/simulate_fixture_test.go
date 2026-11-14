package ethguard_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/spec"
)

// t09SimulateResult starts with the pinned upstream vector; vendor is added
// solely to exercise extension evidence separately from the protected result.
func t09SimulateResult(t *testing.T) json.RawMessage {
	t.Helper()
	data, err := fs.ReadFile(spec.SimulateV1EvidenceFS(), "simulatev1/vectors/ethSimulate-empty.io")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("<< ")) {
			var response struct {
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(line[3:], &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Result) == 0 {
				t.Fatal("official vector has no result")
			}
			return t09SetNestedField(t, response.Result, 0, "vendor", "t09")
		}
	}
	t.Fatal("official vector has no response")
	return nil
}

func t09DecodeSimulate(raw json.RawMessage) (string, error) {
	value, err := ethapi.NewSimulateV1Result(raw)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
func t09TypedWantSimulate(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	got, err := t09DecodeSimulate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func t09TypedSimulate(t *testing.T, f *t09Fleet, params []any) (string, error) {
	t.Helper()
	payload, err := json.Marshal(params[0])
	if err != nil {
		return "", err
	}
	request, err := ethapi.NewSimulateV1Request(payload)
	if err != nil {
		return "", err
	}
	baseJSON, err := json.Marshal(params[1])
	if err != nil {
		return "", err
	}
	base, err := ethapi.NewSimulateBlockReference(baseJSON)
	if err != nil {
		return "", err
	}
	blocks, _, err := f.client.SimulateV1(f.context(t), request, &base)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(blocks)
	return string(encoded), err
}
