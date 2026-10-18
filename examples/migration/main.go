// Minimal migration example: switch code written against go-ethereum's
// ethclient to EthGuard's corroborated, ethclient-shaped adapter.
//
// go run ./examples/migration -config /path/to/config.yaml
//
// What changes: initialization only. ethclient.Dial(url) becomes
// ethadapter.New(cfg) with an explicit upstream list; read calls keep their
// names, signatures and Geth return types (types.Block, *big.Int, ...).
//
// Semantics that differ even where the method exists — read
// docs/design/spec-baseline.md §3 before relying on them:
//
//   - BlockByNumber(ctx, nil) resolves "latest" through a cross-corroborated
//     reference, not one provider's natural head.
//   - Negative block numbers (ethclient: -1 pending, -2 latest, -3 finalized,
//     -4 safe, -5 earliest) are currently NOT mapped the same way here.
//   - ethclient-only methods have no adapter counterpart, and their
//     reachability differs: typed methods that exist only at the root client
//     (NetVersion, FeeHistory) and raw JSON-RPC calls (e.g.
//     SendRawTransaction — forwarded uncorroborated, no transaction lifecycle
//     per decision 0003) go through Unwrap(); Header* reads are approximated
//     by BlockByHash + block.Header(); subscriptions (SubscribeNewHead,
//     SubscribeFilterLogs, ...) have no path at all — the pool is
//     request/response only. See spec-baseline.md §3.2 for the full matrix.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

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
	// Was: client, err := ethclient.Dial("https://rpc.example")
	client, err := ethadapter.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Same call shape and Geth types as ethclient.
	id, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("chain ID corroboration failed: %v", err)
	}
	fmt.Printf("corroborated chain ID: %s\n", id)

	// was: block, err := ec.BlockByNumber(ctx, nil)
	block, err := client.BlockByNumber(ctx, nil)
	if err != nil {
		// ethguard classifies failures; sentinels answer to errors.Is.
		switch {
		case errors.Is(err, ethereum.NotFound):
			log.Print("no block at the corroborated reference")
			return
		default:
			log.Fatalf("corroborated block read failed: %v", err)
		}
	}
	fmt.Printf("corroborated block %d (%s), %d transactions\n",
		block.NumberU64(), block.Hash(), len(block.Transactions()))

	// was: balance, err := ec.BalanceAt(ctx, account, nil)
	var account common.Address // zero address; works for any account
	balance, err := client.BalanceAt(ctx, account, nil)
	if err != nil {
		log.Fatalf("corroborated balance read failed: %v", err)
	}
	fmt.Printf("corroborated zero-address balance: %s wei\n", balance)
}
