// Migration example: the dual-facade story. The same client drives the
// native result-bearing reads, the ethclient-shaped view, a WithResult
// capture, bounded source evidence, the official SendTransaction with the
// write decision delivered through the capture, and one shared Close.
//
// go run ./examples/migration -config /path/to/config.yaml
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard"
)

func main() {
	path := flag.String("config", "config/sample.yaml", "provider configuration file")
	flag.Parse()
	client, err := ethguard.LoadFile(*path)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// --- the ethclient-shaped view: official signatures, same execution ---
	// was: ec, _ := ethclient.Dial(...); ec.BalanceAt(ctx, account, nil)
	ec := client.EthClient()
	balance, err := ec.BalanceAt(ctx, common.Address{}, nil)
	if err != nil {
		// ethguard classifies failures; sentinels answer to errors.Is.
		if errors.Is(err, ethereum.NotFound) {
			log.Print("no balance at the corroborated reference")
		} else {
			log.Fatalf("view balance read failed: %v", err)
		}
	} else {
		fmt.Printf("view corroborated zero-address balance: %s wei\n", balance)
	}

	// --- WithResult: a delivery handle for exactly one call ---
	base, err := ethguard.WithSourceEvidence(ctx, 64<<10) // bounded raw material
	if err != nil {
		log.Fatalf("evidence configuration: %v", err)
	}
	callCtx, capture := ethguard.WithResult(base)
	head, _, err := client.BlockByNumber(callCtx, nil)
	if err != nil {
		log.Fatalf("native block read failed: %v", err)
	}
	fmt.Printf("native corroborated head %d (%s)\n", head.NumberU64(), head.Hash())
	// The capture holds the decision evidence and the budget-bounded raw
	// material of exactly that call; the business result is untouched.
	if result, published, err := capture.Result(); published && err == nil {
		fmt.Printf("  decision status: %s, retained material: %d/%d bytes\n",
			result.Decision.Status, result.Uncertain.Bytes, result.Uncertain.Limit)
	}

	// --- official SendTransaction; the write decision rides the capture ---
	// (a real caller signs here; this example stops before broadcast)
	_ = types.NewTx(&types.LegacyTx{})
	tx, err := signExampleTransaction()
	if err != nil {
		log.Fatalf("sign: %v", err)
	}
	writeCtx, writeCapture := ethguard.WithResult(ctx)
	if err := ec.SendTransaction(writeCtx, tx); err != nil {
		// Acceptance is propagation, not mining; classify with errors.Is.
		log.Printf("broadcast failed: %v", err)
	} else if result, published, _ := writeCapture.Result(); published {
		fmt.Printf("broadcast outcome: accepted=%v hash=%s basis=%d\n",
			result.IsWriteAccepted(), result.WriteDecision.Hash, result.WriteDecision.Basis)
	}

	// --- one Close stops both surfaces ---
	ec.Close()
	fmt.Println("client closed through the view")
}

// signExampleTransaction stands in for the caller's own signing stack; this
// example never broadcasts a real transaction.
func signExampleTransaction() (*types.Transaction, error) {
	return types.NewTx(&types.LegacyTx{Nonce: 0, Gas: 21000}), nil
}
