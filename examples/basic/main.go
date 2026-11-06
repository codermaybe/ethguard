// Basic example: load your providers, perform a corroborated read, and read
// the failure evidence the result carries when the fleet disagrees.
//
// go run ./examples/basic -config /path/to/config.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
)

func main() {
	path := flag.String("config", "config/sample.yaml", "provider configuration file")
	flag.Parse()
	client, err := ethguard.LoadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The native read returns the business value, the execution result, and
	// the trailing error — one call, one execution.
	id, result, err := client.ChainID(ctx)
	if err != nil {
		// The result carries the same conclusion plus the aggregation facts
		// that actually formed: support, reject, faults. A failed aggregation
		// clears the public value; the source tables stay.
		log.Printf("chain ID corroboration failed: %v", err)
		if result.Decision != nil {
			log.Printf("  status=%s support=%d reject=%d faults=%d value=%s",
				result.Decision.Status,
				len(result.Decision.Support),
				len(result.Decision.Reject),
				len(result.Decision.Faults),
				result.Decision.Value)
		}
		return
	}
	fmt.Printf("corroborated chain ID: %s (decision status: %s)\n", id, result.Decision.Status)

	// Anchors are ordinary operations too: one corroborated reference with
	// its establishment evidence on the result.
	ref, anchorResult, err := client.AnchorBlockHash(ctx)
	if err != nil {
		log.Printf("anchor failed: %v", err)
		return
	}
	fmt.Printf("anchor: %s (established by %d sources)\n", ref, len(anchorResult.Decision.Reference.Support))

	// Writes are refused before any network contact — Call is read-only and
	// the raw write has its own entry.
	if _, err := client.Call(ctx, rpc.WriteMethodEthSendRawTransaction, "0x00"); err != nil {
		fmt.Printf("Call correctly refused a write: %v\n", err)
	}
}
