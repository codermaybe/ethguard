// Command rpcsample captures versioned, redacted JSON-RPC wire observations.
// It records evidence; it does not decide whether a provider is compatible.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcsample"
)

func main() {
	planPath := flag.String("plan", "", "path to a v1 sample plan (required)")
	outDir := flag.String("out", "", "new or existing output directory (required)")
	timeout := flag.Duration("timeout", 30*time.Second, "timeout for each HTTP request")
	maxBytes := flag.Int64("max-response-bytes", 0, "response byte limit; 0 preserves the complete body")
	flag.Parse()
	if *planPath == "" || *outDir == "" {
		flag.Usage()
		os.Exit(2)
	}
	plan, err := rpcsample.LoadPlan(*planPath)
	if err != nil {
		fail("load plan: %v", err)
	}
	collector := rpcsample.Collector{Client: &http.Client{Timeout: *timeout}, MaxResponseBytes: *maxBytes}
	index, err := collector.Collect(context.Background(), plan, *outDir)
	if err != nil {
		fail("collect: %v", err)
	}
	fmt.Printf("recorded %d observations in %s; compatibility remains unassessed\n", len(index.Records), *outDir)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rpcsample: "+format+"\n", args...)
	os.Exit(1)
}
