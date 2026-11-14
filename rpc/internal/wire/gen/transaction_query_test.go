package main

import (
	"github.com/codermaybe/ethguard/internal/executionapi"
	"testing"
)

func TestPendingQueryModelSeparatesFromEmbeddedTransaction(t *testing.T) {
	reg := testRegistry()
	raw, err := executionapi.ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	original := testSchema(t, string(raw.Results["eth_getTransactionByHash"]))
	query := testSchema(t, string(effective.Results["eth_getTransactionByHash"]))
	embedded, err := reg.walk(original, "eth_getBlockByHash.result.transaction")
	if err != nil {
		t.Fatal(err)
	}
	standalone, err := reg.walk(query, "eth_getTransactionByHash.result")
	if err != nil {
		t.Fatal(err)
	}
	if embedded.typeName == standalone.typeName {
		t.Fatal("query and embedded shapes share a type")
	}
	for _, obj := range reg.objects {
		for _, f := range obj.fields {
			switch f.json {
			case "blockHash", "blockNumber", "blockTimestamp", "transactionIndex":
				if !f.req || f.ref.pointer != (obj.from[0] == "eth_getTransactionByHash") {
					t.Fatalf("%s.%s lost required or context-specific nullability", obj.name, f.json)
				}
			}
		}
	}
}
