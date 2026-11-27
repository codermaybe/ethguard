package wire_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

// This oracle deliberately retains the original pinned machine schema. It
// rejects pending null metadata, independently exposing the upstream mismatch
// rather than validating the correction against a patched copy of itself.
func TestTransactionQueryRawBundleRemainsVerbatim(t *testing.T) {
	// Header model coverage expands the source envelope without changing the
	// original transaction schema (see the source coverage migration).
	const want = "c600c7959d8ab76884f1db9d8fe5bd2c1a4f4a274afd2fdaf6f8cf5ff454f73a"
	if got := fmt.Sprintf("%x", sha256.Sum256(spec.MethodsJSON())); got != want {
		t.Fatalf("verbatim %s methods bundle changed: %s", spec.Version(), got)
	}
}

func TestTransactionQueryGeneratedPendingContext(t *testing.T) {
	covered := map[string]bool{}
	oracle := officialResultSchemas(t)["eth_getTransactionByHash"]
	parse := resultConstructor(ethapi.NewGetTransactionByHashResult)
	for _, vector := range resultVectors(t, "eth_getTransactionByHash") {
		if vector.Error != nil || ethcodec.IsJSONNull(vector.Result) {
			continue
		}
		var mined map[string]json.RawMessage
		if err := json.Unmarshal(vector.Result, &mined); err != nil {
			t.Fatal(err)
		}
		txType := string(mined["type"])
		if covered[txType] {
			continue
		}
		covered[txType] = true
		t.Run(txType, func(t *testing.T) {
			assertOfficialRoundTrip(t, parse, oracle, vector.Result)
			for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
				mined[key] = json.RawMessage(`null`)
			}
			pending, err := json.Marshal(mined)
			if err != nil {
				t.Fatal(err)
			}
			if err := conformance.ValidateResult(oracle, pending); err == nil {
				t.Fatal("raw pinned schema unexpectedly accepts pending; review fixed correction")
			}
			value, err := ethapi.NewGetTransactionByHashResult(pending)
			if err != nil {
				t.Fatalf("pending rejected: %v", err)
			}
			out, err := ethcodec.MarshalNoEscape(value)
			if err != nil {
				t.Fatal(err)
			}
			var after map[string]json.RawMessage
			if err := json.Unmarshal(out, &after); err != nil {
				t.Fatal(err)
			}
			var expectedContent, actualContent any
			if err := json.Unmarshal(pending, &expectedContent); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &actualContent); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expectedContent, actualContent) {
				t.Fatalf("pending payload/presence changed:\n%s\n%s", pending, out)
			}
			for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
				if string(after[key]) != "null" {
					t.Fatalf("explicit null lost for %s", key)
				}
			}
			again, err := ethapi.NewGetTransactionByHashResult(out)
			if err != nil {
				t.Fatal(err)
			}
			outAgain, err := ethcodec.MarshalNoEscape(again)
			if err != nil || !bytes.Equal(out, outAgain) {
				t.Fatalf("pending round trip not stable: %s (%v)", outAgain, err)
			}
			for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
				t.Run(key, func(t *testing.T) {
					delete(mined, key)
					missing, _ := json.Marshal(mined)
					if _, err := ethapi.NewGetTransactionByHashResult(missing); err == nil {
						t.Fatal("accepted missing required context")
					}
					for _, bad := range []string{`0`, `false`, `[]`, `{}`, `""`, `"0x"`, `"0xgg"`, `"0x-1"`} {
						mined[key] = json.RawMessage(bad)
						invalid, _ := json.Marshal(mined)
						if _, err := ethapi.NewGetTransactionByHashResult(invalid); err == nil {
							t.Fatalf("accepted illegal %s=%s", key, bad)
						}
					}
					mined[key] = json.RawMessage(`null`)
				})
			}
			for _, key := range []string{"from", "hash", "gas", "input", "nonce", "r", "s", "type", "value"} {
				before := mined[key]
				mined[key] = json.RawMessage(`null`)
				invalid, _ := json.Marshal(mined)
				if _, err := ethapi.NewGetTransactionByHashResult(invalid); err == nil {
					t.Fatalf("accepted unrelated null %s", key)
				}
				delete(mined, key)
				invalid, _ = json.Marshal(mined)
				if _, err := ethapi.NewGetTransactionByHashResult(invalid); err == nil {
					t.Fatalf("accepted missing payload required %s", key)
				}
				mined[key] = before
			}
		})
	}
	for _, txType := range []string{`"0x0"`, `"0x1"`, `"0x2"`, `"0x3"`, `"0x4"`} {
		if !covered[txType] {
			t.Errorf("missing official transaction type %s", txType)
		}
	}
	value, err := ethapi.NewGetTransactionByHashResult(json.RawMessage(`null`))
	if err != nil || value != nil {
		t.Fatalf("not-found root changed: %v %v", value, err)
	}
}

func TestTransactionQueryCorrectionDoesNotRelaxEmbeddedBlock(t *testing.T) {
	parsers := generatedResultParsers()
	schemas := officialResultSchemas(t)
	for _, method := range []string{"eth_getBlockByHash", "eth_getBlockByNumber"} {
		t.Run(method, func(t *testing.T) {
			for _, vector := range resultVectors(t, method) {
				if vector.Error != nil || ethcodec.IsJSONNull(vector.Result) {
					continue
				}
				var block map[string]json.RawMessage
				if err := json.Unmarshal(vector.Result, &block); err != nil {
					t.Fatal(err)
				}
				var txs []json.RawMessage
				if err := json.Unmarshal(block["transactions"], &txs); err != nil {
					t.Fatal(err)
				}
				if len(txs) == 0 || bytes.TrimSpace(txs[0])[0] != '{' {
					continue
				}
				assertOfficialRoundTrip(t, parsers[method], schemas[method], vector.Result)
				var tx map[string]json.RawMessage
				if err := json.Unmarshal(txs[0], &tx); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
					before := tx[key]
					tx[key] = json.RawMessage(`null`)
					txs[0], _ = json.Marshal(tx)
					block["transactions"], _ = json.Marshal(txs)
					invalid, _ := json.Marshal(block)
					if _, err := parsers[method](invalid); err == nil {
						t.Fatalf("embedded transaction accepted %s:null", key)
					}
					tx[key] = before
				}
				return
			}
			t.Fatal("no official full transaction block vector")
		})
	}
}
