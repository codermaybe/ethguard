package ethapi_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func TestSimulateGeneratedRequestsPreserveAllOfficialPayloads(t *testing.T) {
	bundle, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(bundle.Requests["eth_simulateV1"][0].Schema, &schema); err != nil {
		t.Fatal(err)
	}
	total, results, errors, unknown := 0, 0, 0, 0
	evidence := spec.SimulateV1EvidenceFS()
	err = fs.WalkDir(evidence, "simulatev1/vectors", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(evidence, name)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			total++
			if vector.Error != nil {
				errors++
			} else {
				results++
			}
			t.Run(vector.Identity(), func(t *testing.T) {
				var request struct{ Params []json.RawMessage }
				if err := json.Unmarshal(vector.Request, &request); err != nil {
					t.Fatal(err)
				}
				if len(request.Params) != 2 {
					t.Fatalf("params=%d, want payload and reference", len(request.Params))
				}
				raw := request.Params[0]
				if err := conformance.ValidateResult(schema, raw); err != nil {
					t.Fatalf("official request schema: %v", err)
				}
				value, err := ethapi.NewSimulateV1Request(raw)
				if err != nil {
					t.Fatalf("valid official payload rejected: %v", err)
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				equal, err := ethcodec.JSONValuesEqual(raw, encoded)
				if err != nil || !equal {
					t.Fatalf("official payload changed: %s -> %s (%v)", raw, encoded, err)
				}
				if err := conformance.ValidateResult(schema, encoded); err != nil {
					t.Fatalf("encoded payload violates official schema: %v", err)
				}
				again, err := ethapi.NewSimulateV1Request(encoded)
				if err != nil {
					t.Fatal(err)
				}
				second, err := json.Marshal(again)
				if err != nil || string(second) != string(encoded) {
					t.Fatal("request encoding is not idempotent")
				}
				base, err := ethapi.NewSimulateBlockReference(request.Params[1])
				if err != nil {
					t.Fatal(err)
				}
				baseWire, err := json.Marshal(base)
				if err != nil || string(baseWire) != string(request.Params[1]) {
					t.Fatalf("official base changed: %s -> %s (%v)", request.Params[1], baseWire, err)
				}
				if strings.Contains(name, "unknown-account-override-key") || strings.Contains(name, "unknown-block-override-key") {
					if vector.Error == nil {
						t.Fatal("unknown-key fixture no longer represents an upstream error")
					}
					unknown++
					var hasExtra bool
					for _, block := range value.BlockStateCalls {
						if block.BlockOverrides != nil && len(block.BlockOverrides.AdditionalProperties) > 0 {
							hasExtra = true
						}
						if block.StateOverrides != nil {
							for _, account := range *block.StateOverrides {
								if len(account.AdditionalProperties) > 0 {
									hasExtra = true
								}
							}
						}
					}
					if !hasExtra {
						t.Fatal("official unknown override key was discarded")
					}
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 95 || results != 64 || errors != 31 || unknown != 2 {
		t.Fatalf("request accounting total=%d results=%d errors=%d unknown-key errors=%d", total, results, errors, unknown)
	}
}

func TestSimulateRequestPresenceConstraintsAndUnknownSafety(t *testing.T) {
	for _, raw := range []string{
		`{"blockStateCalls":[]}`,
		`{"blockStateCalls":[{}],"validation":false,"traceTransfers":false,"returnFullTransactions":false}`,
		`{"blockStateCalls":[{"stateOverrides":{"0x0000000000000000000000000000000000000001":{"balance":"0x0"}}}]}`,
		`{"blockStateCalls":[{"blockOverrides":{"extra":{"number":900719925474099312345678901234567890}},"calls":[{"extra":[null,true,{"deep":1.0000000000000000000000001}]}]}],"unknown":{"nested":[1,2]}}`,
	} {
		value, err := ethapi.NewSimulateV1Request(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("valid request: %s: %v", raw, err)
		}
		out, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		equal, err := ethcodec.JSONValuesEqual(json.RawMessage(raw), out)
		if err != nil || !equal {
			t.Fatalf("request changed: %s -> %s (%v)", raw, out, err)
		}
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"blockStateCalls":null}`, `{"blockStateCalls":[null]}`, `{"blockStateCalls":[1]}`,
		`{"blockStateCalls":[],"validation":null}`,
		`{"blockStateCalls":[{"calls":[{"to":null}]}]}`,
		`{"blockStateCalls":[{"blockOverrides":{"prevRandao":"0x1"}}]}`,
		`{"blockStateCalls":[{"stateOverrides":{"bad":{"balance":"0x1"}}}]}`,
		`{"blockStateCalls":[{"stateOverrides":{"0x0000000000000000000000000000000000000001":{"state":{},"stateDiff":{}}}}]}`,
		`{"blockStateCalls":[{"stateOverrides":{"0x0000000000000000000000000000000000000001":{"state":{"0x1":"0x00"}}}}]}`,
		`{"blockStateCalls":[{"calls":[{"blobVersionedHashes":"0x1"}]}]}`,
		`{"blockStateCalls":[{"calls":[{"accessList":[{"address":"0x0000000000000000000000000000000000000001","storageKeys":[],"unknown":1}]}]}]}`,
		`{"blockStateCalls":[],"blockStateCalls":[]}`,
		`{"blockStateCalls":[],"unknown":{"duplicate":1,"duplicate":2}}`,
	} {
		if _, err := ethapi.NewSimulateV1Request(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid request admitted: %s", raw)
		}
	}
	value, err := ethapi.NewSimulateV1Request(json.RawMessage(`{"blockStateCalls":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, extras := range []map[string]json.RawMessage{
		{"blockStateCalls": json.RawMessage(`[]`)}, {"validation": json.RawMessage(`false`)},
		{"extra": json.RawMessage(`invalid`)}, {"extra": json.RawMessage(`{"x":1,"x":2}`)},
	} {
		value.AdditionalProperties = extras
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("unsafe/colliding extras encoded: %+v", extras)
		}
	}
	account, err := ethapi.NewAccountOverride(json.RawMessage(`{"BALANCE":"unknown","balance":"0x1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if account.Balance == nil || *account.Balance != "0x1" || string(account.AdditionalProperties["BALANCE"]) != `"unknown"` {
		t.Fatal("case-sensitive unknown property was changed")
	}
}

func TestSimulateReferencePreservesHashIdentityAndClosedObjectForms(t *testing.T) {
	leading := "0x" + strings.Repeat("0", 63) + "1"
	zero := "0x" + strings.Repeat("0", 64)
	nonzero := "0x" + strings.Repeat("1", 64)
	for _, raw := range []string{`"latest"`, `"earliest"`, `"safe"`, `"finalized"`, `"pending"`, `"0x0"`, `"0x1"`, `"` + leading + `"`, `"` + zero + `"`, `"` + nonzero + `"`, `{"blockNumber":"0x0"}`, `{"blockHash":"` + leading + `","requireCanonical":false}`} {
		value, err := ethapi.NewSimulateBlockReference(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("valid reference rejected: %s: %v", raw, err)
		}
		out, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		equal, err := ethcodec.JSONValuesEqual(json.RawMessage(raw), out)
		if err != nil || !equal {
			t.Fatalf("reference identity changed: %s -> %s (%v)", raw, out, err)
		}
		if raw == `"`+leading+`"` || raw == `"`+zero+`"` {
			if value.BlockHash == nil || value.BlockNumber != nil {
				t.Fatalf("leading-zero hash selected wrong branch: %+v", value)
			}
		}
	}
	for _, raw := range []string{`null`, `1`, `"unknown"`, `"0x01"`, `{}`, `{"blockNumber":"0x1","blockHash":"` + leading + `"}`, `{"blockNumber":"0x1","requireCanonical":true}`, `{"blockHash":"` + leading + `","extra":1}`} {
		if _, err := ethapi.NewSimulateBlockReference(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid reference admitted: %s", raw)
		}
	}
}
