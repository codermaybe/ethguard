package main

import (
	"testing"

	"github.com/codermaybe/ethguard/internal/executionapi"
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
	block := testSchema(t, string(raw.Results["eth_getBlockByHash"]))
	var original *schema
	for _, property := range block.branches[1].properties {
		if property.name == "transactions" {
			original = property.schema.branches[1].items
		}
	}
	if original == nil {
		t.Fatal("missing official block transactions")
	}
	query := testSchema(t, string(effective.Results["eth_getTransactionByHash"]))
	embeddedPath := "eth_getBlockByHash.result/oneOf/1/properties/transactions/anyOf/1/items"
	if err := reg.registerModelNames(original, embeddedPath); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerModelNames(query, "eth_getTransactionByHash.result"); err != nil {
		t.Fatal(err)
	}
	embedded, err := reg.walk(original, embeddedPath)
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
