package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

func main() {
	out := flag.String("out", "types.gen.go", "output file (relative to the generator's working directory)")
	flag.Parse()

	report := &genReport{visited: map[string]bool{}}
	reg := newRegistry(report)

	bundle, err := executionapi.Read()
	if err != nil {
		fail("%v", err)
	}
	results := bundle.Results
	for _, applied := range bundle.Errata {
		report.errata = append(report.errata, applied.ID+" SHA-256 "+applied.SHA256)
	}

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
		rootRef, err := reg.walk(node, path)
		if err != nil {
			fail("spec: %s: %v", name, err)
		}
		roots[name] = rootRef
		report.methods = append(report.methods, name)
	}

	// Public call input is the unmodified official eth_call parameter schema.
	// Naming is API policy; its fields and constraints are owned by the source.
	callParams := bundle.Requests["eth_call"]
	if len(callParams) == 0 {
		fail("spec: eth_call has no parameter schema")
	}
	node, err := parseSchema(callParams[0].Schema)
	if err != nil {
		fail("spec: eth_call params[0]: %v", err)
	}
	if _, err := reg.walk(node, "eth_call.params[0]"); err != nil {
		fail("%v", err)
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

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}
