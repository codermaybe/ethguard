package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/codermaybe/ethguard/internal/executionapi"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestSchemaPointerRejectsInvalidPathsAndContractDrift(t *testing.T) {
	const fixture = `{"oneOf":[{"type":"boolean"},{"type":"string"},{"type":"object","properties":{"a/b~c":{"type":"boolean"}}}]}`
	for _, pointer := range []string{"oneOf/2", "/oneOf/-1", "/oneOf/02", "/oneOf/3", "/oneOf/-", "/oneOf/2/missing", "/oneOf/2/type/x", "/oneOf/2/properties/a~2b", "/oneOf/2/type", "/oneOf/2/properties/a~"} {
		if _, err := schemaAtPointer(json.RawMessage(fixture), pointer); err == nil {
			t.Fatalf("accepted bad pointer %q", pointer)
		}
	}
	if _, err := schemaAtPointer(json.RawMessage(fixture), "/oneOf/2/properties/a~1b~0c"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", `null`, `{"bad":`, `[]`} {
		if _, err := schemaAtPointer(json.RawMessage(raw), ""); err == nil {
			t.Fatalf("accepted bad schema %q", raw)
		}
	}
	b, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	root := requestRoot{Method: "eth_subscribe", Parameter: 1, Pointer: "/oneOf/2", ExpectedSHA256: "d2e56b0d79a7b0c5d2a7bf9c501354414a12c33ceb37c2b76bb556826a6a9a3d"}
	selected, path, err := selectRequestRoot(b.Requests, root)
	if err != nil {
		t.Fatal(err)
	}
	if path != "eth_subscribe.params[1]/oneOf/2" || !bytes.Contains(selected, []byte(`"transactionHashes"`)) {
		t.Fatal("wrong subscription child")
	}
	root.Pointer = "/oneOf/0"
	if _, _, err := selectRequestRoot(b.Requests, root); err == nil || !strings.Contains(err.Error(), "contract changed") {
		t.Fatalf("moved branch accepted: %v", err)
	}
	root.Pointer = "/oneOf/2"
	root.Parameter = 9
	if _, _, err := selectRequestRoot(b.Requests, root); err == nil {
		t.Fatal("missing parameter accepted")
	}
}

func TestSelectedRequestNodeAuditPinsTheActualNode(t *testing.T) {
	b, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	generated, auditPath := filepath.Join(dir, "types.gen.go"), filepath.Join(dir, "types.audit.json")
	if err := os.WriteFile(generated, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeGenerationAudit(b, generated, auditPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var audit generationAudit
	if err := json.Unmarshal(raw, &audit); err != nil {
		t.Fatal(err)
	}
	actualRoots := map[string]string{}
	for _, entry := range audit.Roots {
		actualRoots[entry.Path] = entry.SHA256
	}
	if actualRoots["eth_subscribe.params[1]"] != "" {
		t.Fatal("audit recorded the unselected subscription union")
	}
	for _, root := range requestRoots {
		selected, path, err := selectRequestRoot(b.Requests, root)
		if err != nil {
			t.Fatal(err)
		}
		if actualRoots[path] != fmt.Sprintf("%x", sha256.Sum256(selected)) {
			t.Fatalf("audit does not bind selected source bytes at %s", path)
		}
		if root.Pointer == "" {
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, selected); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(compact.Bytes())) != root.ExpectedSHA256 {
			t.Fatalf("selected node %s lacks reviewed digest", path)
		}
	}
}

func TestExplicitPublicNamesRejectConflictingShapes(t *testing.T) {
	original := overrideNameByPath
	overrideNameByPath = map[string]string{"a.result": "PinnedModel", "b.result": "PinnedModel"}
	t.Cleanup(func() { overrideNameByPath = original })
	reg := testRegistry()
	a := testSchema(t, `{"type":"object","properties":{"a":{"type":"boolean"}}}`)
	b := testSchema(t, `{"type":"object","properties":{"b":{"type":"boolean"}}}`)
	if err := reg.registerModelNames(a, "a.result"); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerModelNames(b, "b.result"); err == nil || !strings.Contains(err.Error(), "naming conflict") {
		t.Fatalf("different shapes claimed one public name: %v", err)
	}
	if _, err := reg.walk(a, "a.result"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.walk(b, "b.result"); err == nil {
		t.Fatal("walk silently suffixed an explicit public name")
	}
}

func TestTransactionNamesDoNotDependOnMethodTraversalOrder(t *testing.T) {
	b, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"eth_getTransactionByHash", "eth_getBlockByHash", "eth_getBlockByNumber"}
	for _, reverse := range []bool{false, true} {
		reg := testRegistry()
		for _, name := range names {
			node := testSchema(t, string(b.Results[name]))
			if err := reg.registerModelNames(node, name+".result"); err != nil {
				t.Fatal(err)
			}
		}
		if reverse {
			names[0], names[2] = names[2], names[0]
		}
		for _, name := range names {
			if _, err := reg.walk(testSchema(t, string(b.Results[name])), name+".result"); err != nil {
				t.Fatal(err)
			}
		}
		got := map[string]bool{}
		for _, obj := range reg.objects {
			got[obj.name] = true
		}
		for _, union := range reg.unionList {
			got[union.name] = true
		}
		for _, prefix := range []string{"", "Block"} {
			for _, name := range []string{"Transaction", "SetCodeTransaction", "BlobTransaction", "EIP1559Transaction", "AccessListTransaction", "LegacyTransaction"} {
				if !got[prefix+name] {
					t.Fatalf("reverse=%v missing stable %s", reverse, prefix+name)
				}
			}
		}
		if len(reg.report.nameCollisions) != 0 {
			t.Fatalf("public transaction names required collision suffixes: %+v", reg.report.nameCollisions)
		}
	}
}
