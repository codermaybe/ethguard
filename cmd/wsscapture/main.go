// Command wsscapture records opt-in WSS provider evidence and replays it
// offline. It is a manual diagnostic: it never runs in the default test suite,
// it never writes captures into the repository, and it does not decide
// provider compatibility or upgrade decision 0056.
//
// The capture mode dials explicit endpoints, sends read-only probes, and
// writes redacted raw response frames to a caller-selected directory. The
// replay mode reads that directory without network access and checks the
// captured envelopes through rpcwire, aggregator.ObservationRPCError, and the
// real eth_call policy.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	endpoints := flag.String("endpoint", "", "comma-separated ws/wss endpoints (kept in memory only)")
	endpointEnv := flag.String("endpoint-env", "", "comma-separated environment names holding endpoints")
	out := flag.String("out", "", "new or existing capture directory (required for capture)")
	replayDir := flag.String("replay", "", "capture directory to replay offline; disables capture")
	redactEnv := flag.String("redact-env", "", "comma-separated environment names whose values must be redacted")
	providerClass := flag.String("provider-class", "public-unauthenticated", "provider class recorded in evidence")
	timeout := flag.Duration("timeout", 12*time.Second, "per dial/write/read timeout")
	pause := flag.Duration("pause", 250*time.Millisecond, "pause between scenario dials")
	chainID := flag.Uint64("chain", 1, "expected chain id")
	flag.Parse()

	if *replayDir != "" {
		if err := replayDirectory(*replayDir, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "wsscapture: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	urls, err := resolveEndpoints(*endpoints, *endpointEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wsscapture: %v\n", err)
		os.Exit(1)
	}
	cfg := captureConfig{
		Timeout:       *timeout,
		Pause:         *pause,
		ChainID:       *chainID,
		ProviderClass: *providerClass,
		RedactEnv:     splitList(*redactEnv),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := capture(ctx, urls, *out, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "wsscapture: %v\n", err)
		os.Exit(1)
	}
}

func resolveEndpoints(inline, envNames string) ([]string, error) {
	var urls []string
	for _, raw := range splitList(inline) {
		urls = append(urls, raw)
	}
	for _, name := range splitList(envNames) {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("environment variable %s is empty", name)
		}
		urls = append(urls, splitList(value)...)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("at least one -endpoint or -endpoint-env is required")
	}
	return urls, nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
