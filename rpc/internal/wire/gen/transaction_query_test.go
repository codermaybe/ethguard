package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
)

func pinnedQuerySchema(t *testing.T) *schema {
	t.Helper()
	var bundle struct {
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(spec.MethodsJSON(), &bundle); err != nil {
		t.Fatal(err)
	}
	return testSchema(t, string(bundle.Results["eth_getTransactionByHash"]))
}

func TestPendingQueryCorrectionOnlyChangesFourRequiredValues(t *testing.T) {
	raw := append([]byte(nil), spec.MethodsJSON()...)
	original, query := pinnedQuerySchema(t), pinnedQuerySchema(t)
	if err := allowPendingTransactionQueryContext(spec.Version(), query); err != nil {
		t.Fatal(err)
	}
	if fingerprint(original) == fingerprint(query) {
		t.Fatal("query and block shapes would still deduplicate")
	}
	changed := 0
	for i, p := range query.branches[1].properties {
		switch p.name {
		case "blockHash", "blockNumber", "blockTimestamp", "transactionIndex":
			if p.schema.compKind != "oneOf" || len(p.schema.branches) != 2 || !isNullBranch(p.schema.branches[0]) {
				t.Fatalf("%s: expected explicit null plus original value", p.name)
			}
			query.branches[1].properties[i].schema = p.schema.branches[1]
			changed++
		}
	}
	if changed != 4 || fingerprint(original) != fingerprint(query) {
		t.Fatal("correction changed semantics beyond four nullable wrappers")
	}
	if !bytes.Equal(raw, spec.MethodsJSON()) {
		t.Fatal("generation correction mutated the raw bundle")
	}
}

func TestPendingQueryCorrectionRejectsSourceDrift(t *testing.T) {
	cases := map[string]func(*schema){
		"root composition":     func(n *schema) { n.compKind = "anyOf" },
		"root branch order":    func(n *schema) { n.branches[0], n.branches[1] = n.branches[1], n.branches[0] },
		"context optional":     func(n *schema) { n.branches[1].required = n.branches[1].required[1:] },
		"context pattern":      func(n *schema) { n.branches[1].properties[0].schema.pattern = "^changed$" },
		"context removed":      func(n *schema) { n.branches[1].properties = n.branches[1].properties[1:] },
		"new transaction type": func(n *schema) { n.branches[1].branches = n.branches[1].branches[:4] },
		"payload constraint": func(n *schema) {
			n.branches[1].branches[0].properties[0].schema.hasConst = true
			n.branches[1].branches[0].properties[0].schema.constVal = `[]`
		},
		"unsupported keyword": func(n *schema) { n.unsupported = append(n.unsupported, "futureKeyword") },
		"already nullable": func(n *schema) {
			p := &n.branches[1].properties[0]
			p.schema = &schema{compKind: "oneOf", branches: []*schema{{typeVal: "null"}, p.schema}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			n := pinnedQuerySchema(t)
			mutate(n)
			before := fingerprint(n)
			err := allowPendingTransactionQueryContext(spec.Version(), n)
			if err == nil || !strings.Contains(err.Error(), "source schema fingerprint") {
				t.Fatalf("expected explicit stale policy rejection, got %v", err)
			}
			if fingerprint(n) != before {
				t.Fatal("failed correction partially mutated schema")
			}
		})
	}
	if err := allowPendingTransactionQueryContext("future", pinnedQuerySchema(t)); err == nil {
		t.Fatal("accepted another version")
	}
	if err := allowPendingTransactionQueryContext(spec.Version(), nil); err == nil {
		t.Fatal("accepted nil schema")
	}
	n := pinnedQuerySchema(t)
	if err := allowPendingTransactionQueryContext(spec.Version(), n); err != nil {
		t.Fatal(err)
	}
	if err := allowPendingTransactionQueryContext(spec.Version(), n); err == nil {
		t.Fatal("applied correction twice")
	}
}

func TestPendingQueryModelSeparatesFromEmbeddedTransaction(t *testing.T) {
	reg := testRegistry()
	original, query := pinnedQuerySchema(t), pinnedQuerySchema(t)
	if err := allowPendingTransactionQueryContext(spec.Version(), query); err != nil {
		t.Fatal(err)
	}
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
