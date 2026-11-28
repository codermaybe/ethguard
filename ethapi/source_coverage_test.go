package ethapi_test

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

// Synthetic boundary fixtures are independent of official vector availability.
func TestNewOfficialScalarResultRoots(t *testing.T) {
	for _, raw := range []string{`true`, `false`} {
		if _, err := ethapi.NewListeningResult(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`null`, `"true"`, `0`, `[]`} {
		if _, err := ethapi.NewListeningResult(json.RawMessage(raw)); err == nil {
			t.Fatalf("listening accepted %s", raw)
		}
	}
	for name, parse := range map[string]func(json.RawMessage) (*ethapi.Quantity, error){"hash": ethapi.NewGetBlockTransactionCountByHashResult, "number": ethapi.NewGetBlockTransactionCountByNumberResult} {
		t.Run(name, func(t *testing.T) {
			value, err := parse(json.RawMessage(`null`))
			if err != nil || value != nil {
				t.Fatalf("null result: %v", err)
			}
			for _, raw := range []string{`"0x0"`, `"0xa"`} {
				value, err := parse(json.RawMessage(raw))
				if err != nil || value == nil {
					t.Fatalf("valid result %s: %v", raw, err)
				}
			}
			for _, raw := range []string{`"0x00"`, `"0xA"`, `"0X1"`, `"1"`, `1`} {
				if _, err := parse(json.RawMessage(raw)); err == nil {
					t.Fatalf("invalid result accepted: %s", raw)
				}
			}
		})
	}
	for _, raw := range []string{`"0x0"`, `"0xff"`} {
		if _, err := ethapi.NewPeerCountResult(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`null`, `"0x00"`, `"0xA"`, `"0X1"`, `0`} {
		if _, err := ethapi.NewPeerCountResult(json.RawMessage(raw)); err == nil {
			t.Fatalf("peer count accepted %s", raw)
		}
	}
	value, err := ethapi.NewGetTransactionByBlockHashAndIndexResult(json.RawMessage(`null`))
	if err != nil || value != nil {
		t.Fatalf("not-found transaction: %v", err)
	}
}
