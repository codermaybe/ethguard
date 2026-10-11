// Command specdiff produces a reviewable field-level diff between two pinned
// execution-apis result bundles. It never edits the active bundle.
package main

import (
	"flag"
	"fmt"
	"os"

	"ethguard/internal/spec/conformance"
)

func main() {
	beforePath := flag.String("before", "", "previous methods.json (required)")
	afterPath := flag.String("after", "", "candidate methods.json (required)")
	check := flag.Bool("check", false, "fail unless schema changes have an exact migration manifest and both verification checks")
	migrationPath := flag.String("migration", "", "migration manifest used by -check")
	flag.Parse()
	if *beforePath == "" || *afterPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	before, err := os.ReadFile(*beforePath)
	if err != nil {
		fail("read before: %v", err)
	}
	after, err := os.ReadFile(*afterPath)
	if err != nil {
		fail("read after: %v", err)
	}
	diff, err := conformance.DiffBundles(before, after)
	if err != nil {
		fail("diff: %v", err)
	}
	if *check {
		var manifest []byte
		if *migrationPath != "" {
			manifest, err = os.ReadFile(*migrationPath)
			if err != nil {
				fail("read migration: %v", err)
			}
		}
		if err := conformance.ValidateMigration(diff, manifest); err != nil {
			fail("migration gate: %v", err)
		}
	}
	fmt.Printf("# execution-apis result schema diff\n\n- before: %s\n- after: %s\n", diff.BeforeVersion, diff.AfterVersion)
	if len(diff.Methods) == 0 {
		fmt.Println("\n- no result schema changes")
		if *check {
			fmt.Println("- migration gate: passed")
		}
		return
	}
	if *check {
		fmt.Println("\n- migration gate: passed")
	}
	for _, method := range diff.Methods {
		switch {
		case method.Added:
			fmt.Printf("\n## %s\n\n- method: added\n", method.Method)
		case method.Removed:
			fmt.Printf("\n## %s\n\n- method: removed\n", method.Method)
		default:
			fmt.Printf("\n## %s\n", method.Method)
			for _, change := range method.Changed {
				fmt.Printf("\n- `%s`: %s", change.Path, change.Kind)
				if change.Kind == "changed" {
					fmt.Printf("; type %s -> %s; required %t -> %t", change.BeforeType, change.AfterType, change.BeforeRequired, change.AfterRequired)
				}
				fmt.Println()
			}
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
