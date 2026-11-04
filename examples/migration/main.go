// Minimal migration example: switch code written against go-ethereum's
// ethclient to EthGuard's corroborated client.
//
// go run ./examples/migration -endpoint-a https://a.example:8545 -endpoint-b https://b.example:8545
//
// What changes: initialization only. ethclient.Dial(url) becomes
// ethguard.Dial(endpointA, endpointB) with two or more distinct endpoints;
// read calls keep their names, signatures and Geth return types
// (types.Block, *big.Int, ...).
//
// Semantics that differ even where the method exists — read
// docs/design/method-contracts.md before relying on them:
//
//   - BlockByNumber(ctx, nil) resolves "latest" through a cross-corroborated
//     reference, not one provider's natural head.
//   - Negative block numbers map to their own level: -1 pending is rejected
//     locally (no typed pending block projection), -2 latest, -3 finalized
//     and -4 safe each establish their own level's reference, -5 earliest is
//     the explicit height 0x0.
//   - A single endpoint must opt into Forwarded mode explicitly with
//     ethguard.DialUncorroborated(url); Dial requires two or more distinct
//     endpoints.
//   - Header* reads are approximated by BlockByHash + block.Header();
//     subscriptions (SubscribeNewHead, SubscribeFilterLogs, ...) have no
//     path at all — the pool is request/response only.
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

	"github.com/codermaybe/ethguard"
)

func main() {
	endpointA := flag.String("endpoint-a", "https://a.example:8545", "first RPC endpoint")
	endpointB := flag.String("endpoint-b", "https://b.example:8545", "second RPC endpoint")
	flag.Parse()
	// Was: client, err := ethclient.Dial("https://rpc.example")
	client, err := ethguard.Dial(*endpointA, *endpointB)
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
