// Command specmethod extracts one method's complete definition (result and
// parameter schemas) from an official dereferenced execution-apis
// openrpc.json into a single-method bundle. It is the source pipeline for
// methods whose wire types are code-generated outside methods.json.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/codermaybe/ethguard/internal/spec/builder"
)

func main() {
	openRPCPath := flag.String("openrpc", "", "dereferenced official openrpc.json (required)")
	version := flag.String("version", "", "official release tag (required)")
	name := flag.String("method", "", "method name to extract (required)")
	outPath := flag.String("out", "", "output bundle file (required)")
	flag.Parse()
	if *openRPCPath == "" || *version == "" || *name == "" || *outPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	openRPC, err := os.ReadFile(*openRPCPath)
	if err != nil {
		fail("read openrpc: %v", err)
	}
	bundle, err := builder.BuildMethod(openRPC, *version, *name)
	if err != nil {
		fail("build bundle: %v", err)
	}
	if err := os.WriteFile(*outPath, bundle, 0o644); err != nil {
		fail("write bundle: %v", err)
	}
	fmt.Printf("wrote %s (%s, %s)\n", *outPath, *version, *name)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
