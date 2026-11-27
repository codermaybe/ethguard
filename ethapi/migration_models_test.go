package ethapi_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func TestBlockReferenceSharedAliasAndClosedOfficialForms(t *testing.T) {
	hash := `"0x` + strings.Repeat("0", 64) + `"`
	for _, raw := range []string{`"earliest"`, `"pending"`, `"0x0"`, hash, `{"blockNumber":"0x1"}`, `{"blockHash":` + hash + `,"requireCanonical":false}`} {
		model, err := ethapi.NewBlockReference(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		var alias ethapi.SimulateBlockReference = model
		out, err := json.Marshal(alias)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != raw {
			t.Fatalf("selector changed: %s => %s", raw, out)
		}
		if _, err := ethapi.NewSimulateBlockReference(out); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"blockNumber":"latest"}`, `{"blockNumber":"0x1","requireCanonical":false}`, `{"blockHash":` + hash + `,"blockNumber":"0x1"}`} {
		if _, err := ethapi.NewBlockReference(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid selector %s", raw)
		}
	}
}

func TestFilterQueryPreservesNullableAndNestedChoices(t *testing.T) {
	address := `"0x` + strings.Repeat("1", 40) + `"`
	hash := `"0x` + strings.Repeat("2", 64) + `"`
	for _, raw := range []string{`{}`, `{"address":null}`, `{"address":` + address + `}`, `{"address":[]}`, `{"address":[` + address + `],"topics":null}`, `{"topics":[]}`, `{"topics":[null,` + hash + `,[` + hash + `],[]]}`, `{"fromBlock":"earliest","toBlock":"latest"}`, `{"blockHash":` + hash + `}`, `{"blockHash":` + hash + `,"fromBlock":"0x1"}`} {
		model, err := ethapi.NewFilterQuery(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		out, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("filter presence changed: %s => %s", raw, out)
		}
	}
	// The hash object is officially open. Cross-field method rejection belongs
	// to the method owner; the generator preserves the supplied range member.
	for _, raw := range []string{`null`, `{"fromBlock":"pending"}`, `{"address":{}}`, `{"address":[null]}`, `{"topics":[[null]]}`, `{"topics":false}`, `{"blockHash":null}`} {
		if _, err := ethapi.NewFilterQuery(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted malformed filter %s", raw)
		}
	}
}

func TestTransactionReceiptsQueryOptionalNonnullableList(t *testing.T) {
	hash := `"0x` + strings.Repeat("3", 64) + `"`
	for _, raw := range []string{`{}`, `{"transactionHashes":[]}`, `{"transactionHashes":[` + hash + `]}`} {
		model, err := ethapi.NewTransactionReceiptsQuery(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if (model.TransactionHashes == nil) != (raw == `{}`) {
			t.Fatalf("lost list presence: %s", raw)
		}
		out, err := json.Marshal(model)
		if err != nil || string(out) != raw {
			t.Fatalf("%s => %s: %v", raw, out, err)
		}
	}
	for _, raw := range []string{`null`, `{"transactionHashes":null}`, `{"transactionHashes":[null]}`, `{"transactionHashes":["0x1"]}`, `{"transactionHashes":"all"}`, `{"address":null}`} {
		if _, err := ethapi.NewTransactionReceiptsQuery(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid receipts query %s", raw)
		}
	}
}

func TestHeaderModelRequiresDifficultyAndPreservesForkFields(t *testing.T) {
	vectors, err := conformance.ParseVector(spec.VectorsFS(), "vectors/eth_getBlockByNumber/get-genesis.io")
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(vectors[0].Result, &header); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"transactions", "uncles", "size", "withdrawals"} {
		delete(header, key)
	}
	hash := json.RawMessage(`"0x` + strings.Repeat("4", 64) + `"`)
	for _, key := range []string{"parentBeaconBlockRoot", "requestsHash", "blockAccessListHash", "withdrawalsRoot"} {
		header[key] = hash
	}
	for _, key := range []string{"baseFeePerGas", "blobGasUsed", "excessBlobGas", "slotNumber"} {
		header[key] = json.RawMessage(`"0x1"`)
	}
	raw, _ := json.Marshal(header)
	model, err := ethapi.NewHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(out, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(header, after) {
		t.Fatalf("Header lost official fields: %s", out)
	}
	for _, parse := range []func(json.RawMessage) (*ethapi.Header, error){ethapi.NewGetHeaderByHashResult, ethapi.NewGetHeaderByNumberResult} {
		if h, err := parse(raw); err != nil || h == nil {
			t.Fatalf("Header result: %v", err)
		}
		if h, err := parse(json.RawMessage(`null`)); err != nil || h != nil {
			t.Fatalf("nullable Header result: %v", err)
		}
	}
	delete(header, "difficulty")
	bad, _ := json.Marshal(header)
	if _, err := ethapi.NewHeader(bad); err == nil {
		t.Fatal("Header accepted missing required difficulty")
	}
	header["difficulty"] = json.RawMessage(`null`)
	bad, _ = json.Marshal(header)
	if _, err := ethapi.NewHeader(bad); err == nil {
		t.Fatal("Header accepted null difficulty")
	}
}

func TestTransactionStableModelsRetainPendingContextIsolation(t *testing.T) {
	vectors, err := conformance.ReadVectors(spec.VectorsFS())
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, v := range vectors {
		if v.Method != "eth_getTransactionByHash" || v.Error != nil || bytes.Equal(bytes.TrimSpace(v.Result), []byte("null")) {
			continue
		}
		var tx map[string]json.RawMessage
		if err := json.Unmarshal(v.Result, &tx); err != nil {
			t.Fatal(err)
		}
		kind := string(tx["type"])
		if covered[kind] {
			continue
		}
		covered[kind] = true
		t.Run(kind, func(t *testing.T) {
			query, err := ethapi.NewTransaction(v.Result)
			if err != nil {
				t.Fatal(err)
			}
			byIndex, err := ethapi.NewGetTransactionByBlockHashAndIndexResult(v.Result)
			if err != nil || byIndex == nil {
				t.Fatalf("new transaction method root rejected mined fixture: %v", err)
			}
			block, err := ethapi.NewBlockTransaction(v.Result)
			if err != nil {
				t.Fatal(err)
			}
			qvalue := reflect.ValueOf(query)
			bvalue := reflect.ValueOf(block)
			selected := -1
			for i := 0; i < qvalue.NumField(); i++ {
				if !qvalue.Field(i).IsNil() {
					selected = i
				}
			}
			if selected < 0 || bvalue.Field(selected).IsNil() {
				t.Fatal("query and block selected different branches")
			}
			for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
				tx[key] = json.RawMessage(`null`)
			}
			pending, _ := json.Marshal(tx)
			if _, err := ethapi.NewTransaction(pending); err != nil {
				t.Fatalf("query pending rejected: %v", err)
			}
			if _, err := ethapi.NewGetTransactionByBlockHashAndIndexResult(pending); err == nil {
				t.Fatal("by-block-index root inherited pending erratum")
			}
			if _, err := ethapi.NewBlockTransaction(pending); err == nil {
				t.Fatal("embedded model inherited pending erratum")
			}
			delete(tx, "blockHash")
			missing, _ := json.Marshal(tx)
			if _, err := ethapi.NewTransaction(missing); err == nil {
				t.Fatal("pending query accepted missing inclusion member")
			}
		})
	}
	if len(covered) != 5 {
		t.Fatalf("covered transaction types %v", covered)
	}
}
