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
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

func main() {
	path := flag.String("config", "config/sample.yaml", "provider configuration file")
	flag.Parse()
	if err := run(*path); err != nil {
		log.Fatal(err)
	}
}

func run(path string) error {
	client, err := ethguard.LoadFile(path)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// --- the ethclient-shaped view: official signatures, same execution ---
	// was: ec, _ := ethclient.Dial(...); ec.BalanceAt(ctx, account, nil)
	// Scalar inputs are the generated ethapi.Address/ethapi.Hash; the
	// explicit all-zero address is legal, the empty string is not.
	ec := client.EthClient()
	zeroAccount := ethapi.Address("0x0000000000000000000000000000000000000000")
	balance, err := ec.BalanceAt(ctx, zeroAccount, nil)
	if err != nil {
		// ethguard classifies failures; sentinels answer to errors.Is.
		if errors.Is(err, ethereum.NotFound) {
			log.Print("no balance at the corroborated reference")
		} else {
			return fmt.Errorf("view balance read failed: %w", err)
		}
	} else {
		fmt.Printf("view corroborated zero-address balance: %s wei\n", balance)
	}

	// --- WithResult: a delivery handle for exactly one call ---
	base, err := ethguard.WithSourceEvidence(ctx, 64<<10) // bounded raw material
	if err != nil {
		return fmt.Errorf("evidence configuration: %w", err)
	}
	callCtx, capture := ethguard.WithResult(base)
	head, _, err := client.BlockByNumber(callCtx, nil)
	if err != nil {
		return fmt.Errorf("native block read failed: %w", err)
	}
	headHeight, heightErr := rpc.ParseHexQuantity(string(head.Number))
	if heightErr != nil {
		return fmt.Errorf("native block height: %w", heightErr)
	}
	fmt.Printf("native corroborated head %d (%s)\n", headHeight, head.Hash)
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
		return fmt.Errorf("sign: %w", err)
	}
	writeCtx, writeCapture := ethguard.WithResult(ctx)
	if err := ec.SendTransaction(writeCtx, tx); err != nil {
		// Acceptance is propagation, not mining; classify with errors.Is.
		log.Printf("broadcast failed: %v", err)
	} else if result, published, _ := writeCapture.Result(); published {
		fmt.Printf("broadcast outcome: accepted=%v hash=%s basis=%d\n",
			result.IsWriteAccepted(), result.WriteDecision.Hash, result.WriteDecision.Adjudication.Basis)
	}

	// --- one Close stops both surfaces ---
	ec.Close()
	fmt.Println("client closed through the view")
	return nil
}

// signExampleTransaction stands in for the caller's own signing stack; this
// example never broadcasts a real transaction.
func signExampleTransaction() (*types.Transaction, error) {
	return types.NewTx(&types.LegacyTx{Nonce: 0, Gas: 21000}), nil
}
