// Basic example: load your providers and perform an ethclient-shaped read.
//
// go run ./examples/basic -config /path/to/config.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethadapter"
)

func main() {
	path := flag.String("config", "config/sample.yaml", "provider configuration file")
	flag.Parse()
	cfg, err := config.LoadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	client, err := ethadapter.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := client.ChainID(ctx)
	if err != nil {
		log.Printf("chain ID corroboration failed: %v", err)
		return
	}
	fmt.Printf("corroborated chain ID: %s\n", id)
}
