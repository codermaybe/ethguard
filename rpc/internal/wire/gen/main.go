package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/internal/spec"
)

func main() {
	out := flag.String("out", "types.gen.go", "output file (relative to the generator's working directory)")
	flag.Parse()

	report := &genReport{visited: map[string]bool{}, patternVars: map[string]string{}}
	reg := newRegistry(report)

	// Method result bundle (23 methods) + the single-method simulate bundle.
	var bundle struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(spec.MethodsJSON(), &bundle); err != nil {
		fail("spec bundle: %v", err)
	}
	if bundle.Version != spec.Version() {
		fail("spec bundle: methods.json version %q != VERSION pin %q", bundle.Version, spec.Version())
	}
	var simulate struct {
		Version string          `json:"version"`
		Method  string          `json:"method"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(spec.SimulateV1JSON(), &simulate); err != nil {
		fail("spec bundle: simulatev1: %v", err)
	}
	if simulate.Version != bundle.Version {
		fail("simulatev1 bundle version %q != methods bundle version %q", simulate.Version, bundle.Version)
	}
	if _, exists := bundle.Results[simulate.Method]; exists {
		fail("spec bundle: %s is already inside methods.json; remove the simulatev1 bundle", simulate.Method)
	}
	results := bundle.Results
	results[simulate.Method] = simulate.Result

	// Walk every method result root in deterministic order.
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)
	roots := make(map[string]*ref, len(results))
	for _, name := range names {
		node, err := parseSchema(results[name])
		if err != nil {
			fail("spec: %s: %v", name, err)
		}
		path := name + ".result"
		override, hasOverride := rootOverrides[name]
		if hasOverride {
			node, err = selectBranch(node, override)
			if err != nil {
				fail("spec: %s: %v", name, err)
			}
			path += "." + override
		}
		if hasOverride {
			report.visited[path] = true
		}
		rootRef, err := reg.walk(node, path)
		if err != nil {
			fail("spec: %s: %v", name, err)
		}
		roots[name] = rootRef
		report.methods = append(report.methods, name)
	}

	if err := emit(reg, bundle.Version, roots, *out); err != nil {
		fail("%v", err)
	}
	abs, _ := filepath.Abs(*out)
	fmt.Fprintf(os.Stderr, "gen: wrote %s (%d objects, %d fields, %d unions, %d shims)\n",
		abs, reg.report.objects, reg.report.countFields, len(reg.unionList), reg.report.countShims)
	for _, c := range reg.report.nameCollisions {
		fmt.Fprintf(os.Stderr, "gen: collision %s -> %s at %s\n", c.original, c.resolved, c.path)
	}
	for path, name := range overrideNameByPath {
		if !report.visited[path] {
			fmt.Fprintf(os.Stderr, "gen: warning: override %q -> %q unused\n", path, name)
		}
	}
}

// selectBranch resolves a "oneOf[i]" root override against a parsed schema.
func selectBranch(node *schema, selector string) (*schema, error) {
	composition := selector
	i := strings.LastIndexByte(selector, '[')
	if i < 0 || !strings.HasPrefix(selector, "oneOf") {
		return nil, fmt.Errorf("root override %q must look like oneOf[i]", selector)
	}
	composition = selector[:i]
	index, err := strconv.Atoi(strings.TrimSuffix(selector[i+1:], "]"))
	if err != nil {
		return nil, fmt.Errorf("root override %q: %v", selector, err)
	}
	if composition != "oneOf" && composition != "anyOf" {
		return nil, fmt.Errorf("root override %q: unsupported composition", selector)
	}
	if node.typeVal == "object" && len(node.properties) > 0 {
		return nil, fmt.Errorf("root override %q: node is an object, override expects a composition", selector)
	}
	if len(node.branches) <= index || index < 0 {
		return nil, fmt.Errorf("root override %q: branch out of range", selector)
	}
	return node.branches[index], nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}
