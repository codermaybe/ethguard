package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

func TestManifestUsageAndPublicCollisionGates(t *testing.T) {
	for _, kind := range []string{"path", "title", "order"} {
		t.Run(kind, func(t *testing.T) {
			manifest := executionapi.Manifest{}
			switch kind {
			case "path":
				manifest.Names = []executionapi.ModelNameRule{{Source: executionapi.SchemaRef{Method: "eth_missing", Part: "result"}, Name: "Missing"}}
			case "title":
				manifest.TitleNames = map[string]string{"missing": "Missing"}
			case "order":
				manifest.FieldOrder = map[string][]string{"Missing": {"field"}}
			}
			reg := newRegistry(manifest, &genReport{visited: map[string]bool{}})
			if err := reg.validatePolicyUsage(); err == nil || !strings.Contains(err.Error(), "unused") {
				t.Fatalf("unused contract accepted: %v", err)
			}
		})
	}
	t.Run("model-title", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{}, &genReport{visited: map[string]bool{}})
		if _, err := reg.walk(testSchema(t, `{"type":"object","title":"Clash","properties":{"a":{"type":"boolean"}}}`), "eth_a.result"); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.walk(testSchema(t, `{"type":"object","title":"Clash","properties":{"b":{"type":"boolean"}}}`), "eth_b.result"); err == nil {
			t.Fatal("silently suffixed a public model")
		}
	})
	t.Run("native", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{}, &genReport{visited: map[string]bool{}})
		if _, err := reg.walk(testSchema(t, `{"type":"object","title":"Hash","properties":{"a":{"type":"boolean"}}}`), "eth_a.result"); err == nil {
			t.Fatal("native collision accepted")
		}
	})
	t.Run("absent-alias", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{Aliases: map[string]string{"Alias": "Missing"}}, &genReport{visited: map[string]bool{}})
		if err := reg.validatePublicNames(nil); err == nil {
			t.Fatal("missing alias accepted")
		}
	})
	t.Run("missing-order-field", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{FieldOrder: map[string][]string{"Fixture": {"absent"}}}, &genReport{visited: map[string]bool{}})
		if _, err := reg.walk(testSchema(t, `{"type":"object","title":"Fixture","properties":{"a":{"type":"boolean"}}}`), "eth_a.result"); err == nil {
			t.Fatal("missing ordered field accepted")
		}
	})
	t.Run("root-constructor", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{}, &genReport{visited: map[string]bool{}})
		method := "eth_getFoo"
		name := strings.TrimPrefix(methodShimName(method), "New")
		reg.objects = []*objectModel{{name: name}}
		if err := reg.validatePublicNames(map[string]*ref{method: {kind: refScalar, native: "bool"}}); err == nil {
			t.Fatal("result shim collision accepted")
		}
	})
	t.Run("union-field", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{}, &genReport{visited: map[string]bool{}})
		if _, err := reg.walk(testSchema(t, `{"title":"Choice","oneOf":[{"title":"Same","type":"boolean"},{"title":"Same","type":"string"}]}`), "eth_a.result"); err == nil {
			t.Fatal("union field silently suffixed")
		}
	})
}

func TestGeneratedOutputsRejectEquivalentTargets(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "same")
	for _, alias := range []string{dir + "/./same", filepath.Join(dir, "child", "..", "same")} {
		same, err := sameOutputFile(target, alias)
		if err != nil || !same {
			t.Fatalf("same output alias %s: %v %v", alias, same, err)
		}
	}
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "hardlink")
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	same, err := sameOutputFile(target, alias)
	if err != nil || !same {
		t.Fatalf("same inode: %v %v", same, err)
	}
}

func TestCompilerRequiresManifestExtractionScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*executionapi.Bundle, *executionapi.Manifest)
		want   string
	}{
		{"missing request", func(b *executionapi.Bundle, _ *executionapi.Manifest) {
			delete(b.Requests, "net_listening")
		}, "missing=[net_listening]"},
		{"extra request", func(b *executionapi.Bundle, _ *executionapi.Manifest) {
			b.Requests["debug_traceTransaction"] = nil
		}, "extra=[debug_traceTransaction]"},
		{"removed prefix", func(_ *executionapi.Bundle, m *executionapi.Manifest) {
			m.RequestInventoryPrefixes = []string{"eth_"}
		}, "extra=[net_listening net_peerCount net_version]"},
		{"added prefix", func(_ *executionapi.Bundle, m *executionapi.Manifest) {
			m.RequestInventoryPrefixes = append(m.RequestInventoryPrefixes, "engine_")
		}, "missing=[engine_"},
		{"unused prefix", func(_ *executionapi.Bundle, m *executionapi.Manifest) {
			m.RequestInventoryPrefixes = append(m.RequestInventoryPrefixes, "fixture_")
		}, "unused request inventory prefix"},
		{"redundant prefix", func(b *executionapi.Bundle, m *executionapi.Manifest) {
			b.Inventory = append(b.Inventory, executionapi.InventoryMethod{Name: "eth_fixture_ping"})
			b.Requests["eth_fixture_ping"] = nil
			m.RequestInventoryPrefixes = append(m.RequestInventoryPrefixes, "eth_fixture_")
		}, "redundant request inventory prefix"},
		{"absent inventory", func(b *executionapi.Bundle, _ *executionapi.Manifest) {
			b.Inventory = nil
		}, "complete upstream inventory is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := executionapi.Read()
			if err != nil {
				t.Fatal(err)
			}
			manifest := testManifest()
			tc.change(&bundle, &manifest)
			if _, _, err := compileBundle(bundle, manifest); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scope mismatch error=%v, want %q", err, tc.want)
			}
		})
	}
}
