// Basic example: load sample config and construct an EthGuard client shell.
//
//	go run ./examples/basic
package main

import (
	"fmt"
	"path/filepath"
	"runtime"

	"ethguard"
	"ethguard/config"
)

func main() {
	// Resolve sample config relative to this file for local demos.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	sample := filepath.Join(filepath.Dir(file), "..", "..", "config", "sample.yaml")

	cfg, err := config.LoadFile(sample)
	if err != nil {
		panic(err)
	}

	client, err := ethguard.New(cfg)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	fmt.Printf("ethguard client ready (upstreams=%d, pool=%v)\n", len(cfg.Upstreams), client.Pool() != nil)
}
