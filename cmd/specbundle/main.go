// Command specbundle rebuilds the pinned methods.json subset from an official
// dereferenced execution-apis openrpc.json.
package main

import (
	"flag"
	"fmt"
	"os"

	"ethguard/internal/spec/builder"
)

func main() {
	openRPCPath := flag.String("openrpc", "", "dereferenced official openrpc.json (required)")
	version := flag.String("version", "", "official release tag (required)")
	methodsPath := flag.String("methods-from", "", "existing methods.json whose method set is preserved (required)")
	outPath := flag.String("out", "", "output methods.json (required)")
	flag.Parse()
	if *openRPCPath == "" || *version == "" || *methodsPath == "" || *outPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	openRPC, err := os.ReadFile(*openRPCPath)
	if err != nil {
		fail("read openrpc: %v", err)
	}
	base, err := os.ReadFile(*methodsPath)
	if err != nil {
		fail("read methods bundle: %v", err)
	}
	methods, err := builder.MethodsFromBundle(base)
	if err != nil {
		fail("read method set: %v", err)
	}
	bundle, err := builder.Build(openRPC, *version, methods)
	if err != nil {
		fail("build bundle: %v", err)
	}
	if err := os.WriteFile(*outPath, bundle, 0o644); err != nil {
		fail("write bundle: %v", err)
	}
	fmt.Printf("wrote %s (%s, %d methods)\n", *outPath, *version, len(methods))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
