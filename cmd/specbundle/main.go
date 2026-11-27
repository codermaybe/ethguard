// Command specbundle extracts original schemas using the shared build Manifest.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec/builder"
)

func main() {
	openRPCPath := flag.String("openrpc", "", "dereferenced official OpenRPC input")
	version := flag.String("version", "", "fixed complete upstream commit")
	manifestPath := flag.String("manifest", "internal/executionapi/manifest.json", "shared generation contract")
	outPath := flag.String("out", "", "output methods.json")
	requestsPath := flag.String("requests-out", "", "output request inventory")
	inventoryPath := flag.String("inventory-out", "", "output complete upstream inventory")
	flag.Parse()
	if *openRPCPath == "" || *version == "" || *outPath == "" || *requestsPath == "" || *inventoryPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	raw, err := os.ReadFile(*openRPCPath)
	if err != nil {
		fail("read OpenRPC: %v", err)
	}
	manifest, err := executionapi.LoadManifest(*manifestPath)
	if err != nil {
		fail("manifest: %v", err)
	}
	artifacts, err := builder.BuildSelected(raw, *version, manifest)
	if err != nil {
		fail("extract: %v", err)
	}
	for _, output := range []struct {
		path string
		raw  []byte
	}{{*outPath, artifacts.Methods}, {*requestsPath, artifacts.Requests}, {*inventoryPath, artifacts.Inventory}} {
		if err := os.WriteFile(output.path, output.raw, 0644); err != nil {
			fail("write %s: %v", output.path, err)
		}
	}
	fmt.Printf("wrote original result, request and complete inventory at %s\n", *version)
}
func fail(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
