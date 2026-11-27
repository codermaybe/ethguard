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
	auditOut := flag.String("audit-out", "", "also write the deterministic schema and generation-policy audit")
	flag.Parse()

	report := &genReport{visited: map[string]bool{}}
	reg := newRegistry(report)
	if _, err := reviewedScalarPolicies(); err != nil {
		fail("policy: %v", err)
	}

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
	for _, name := range names {
		node, err := parseSchema(results[name])
		if err != nil {
			fail("spec: %s: %v", name, err)
		}
		if err := reg.registerModelNames(node, name+".result"); err != nil {
			fail("%v", err)
		}
	}
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

	// Every selected request root has the same construction policy. The
	// selection controls public generation coverage, never method semantics.
	if err := generateRequestRoots(reg, bundle.Requests); err != nil {
		fail("%v", err)
	}

	if err := emit(reg, bundle.Version, roots, *out); err != nil {
		fail("%v", err)
	}
	if *auditOut != "" {
		if err := writeGenerationAudit(bundle, *out, *auditOut); err != nil {
			fail("audit: %v", err)
		}
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
