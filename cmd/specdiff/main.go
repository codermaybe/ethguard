// Command specdiff compares original result and request bundles together.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func main() {
	before := flag.String("before-dir", "", "previous spec root")
	after := flag.String("after-dir", "", "candidate spec root")
	jsonOutput := flag.Bool("json", false, "emit complete machine-readable diff")
	check := flag.Bool("check", false, "fail if any definition or representation differs")
	flag.Parse()
	if *before == "" || *after == "" {
		flag.Usage()
		os.Exit(2)
	}
	diff, err := conformance.DiffDirectories(os.DirFS(*before), os.DirFS(*after))
	if err != nil {
		fail("diff: %v", err)
	}
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(diff); err != nil {
			fail("output: %v", err)
		}
	} else {
		fmt.Printf("execution-apis %s -> %s\n", diff.BeforeVersion, diff.AfterVersion)
		for _, part := range []struct {
			name    string
			methods []conformance.MethodDiff
		}{{"result", diff.Results}, {"request", diff.Requests}} {
			for _, method := range part.methods {
				fmt.Printf("%s %s: added=%t removed=%t changes=%d\n", part.name, method.Method, method.Added, method.Removed, len(method.Changed))
				for _, change := range method.Changed {
					fmt.Printf("  %s %s\n", change.Kind, change.Path)
				}
			}
		}
		for _, artifact := range diff.Artifacts {
			if artifact.Changed {
				fmt.Printf("bytes changed: %s\n", artifact.Name)
			}
		}
	}
	if *check {
		for _, artifact := range diff.Artifacts {
			if artifact.Changed {
				fail("source changed; review diff and specsync execution evidence")
			}
		}
	}
}
func fail(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
