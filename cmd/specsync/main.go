// Command specsync builds and validates isolated execution-apis candidates.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/codermaybe/ethguard/internal/specsync"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var options specsync.Options
	flag.StringVar(&options.Inputs.Commit, "commit", "", "online: full official commit")
	flag.StringVar(&options.Inputs.UpstreamDir, "upstream-dir", "", "offline: clean official local checkout")
	flag.StringVar(&options.Inputs.OpenRPC, "openrpc", "", "offline: dereferenced OpenRPC file")
	flag.StringVar(&options.Inputs.VectorsDir, "vectors-dir", "", "offline: original vector directory")
	flag.StringVar(&options.Inputs.Source, "source", "", "offline: reviewed material source and vector metadata")
	flag.StringVar(&options.Workspace, "workspace", ".", "actual source workspace to snapshot, including uncommitted changes")
	flag.StringVar(&options.Manifest, "manifest", "", "shared manifest (defaults to workspace contract)")
	flag.StringVar(&options.Out, "out", "", "new candidate directory outside workspace")
	flag.StringVar(&options.GoTool, "go-tool", "", "installed Go executable (default: local runtime toolchain or PATH; never downloads a toolchain)")
	flag.BoolVar(&options.PrepareOnly, "prepare-only", false, "prepare schemas and diagnostics without claiming validation or readiness")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, err := specsync.Run(ctx, options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "specsync: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("candidate: %s (ready=%t, publishable=%t); review report.json\n", options.Out, report.Ready, report.Publishable)
}
