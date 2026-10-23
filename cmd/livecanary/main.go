// Command livecanary generates live-canary manifests. The manifest subcommand
// is offline and consumes a verified snapshot descriptor; fetch performs one
// Chainlist acquisition and fixes the run's membership. Exit code 2 means the
// pre-plan acquisition is inconclusive (registry unavailable or too few
// members), with manifest/artifact still written when available.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/codermaybe/ethguard/internal/livecanary"
)

type dependencies struct {
	loadSnapshot func(string) (*livecanary.LoadedSnapshot, error)
	detect       func() (livecanary.Provenance, error)
	newFetcher   func() livecanary.Fetcher
}

type inconclusiveError struct {
	reason string
}

func (e *inconclusiveError) Error() string {
	return "inconclusive: " + e.reason
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "livecanary: %v\n", err)
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var inconclusive *inconclusiveError
	if errors.As(err, &inconclusive) {
		return 2
	}
	return 1
}

func run(args []string, stdout io.Writer) error {
	return runWith(args, stdout, defaultDependencies())
}

func defaultDependencies() dependencies {
	return dependencies{
		loadSnapshot: livecanary.LoadSnapshot,
		detect: func() (livecanary.Provenance, error) {
			var detector livecanary.Detector
			return detector.Detect()
		},
		newFetcher: func() livecanary.Fetcher {
			return livecanary.Fetcher{}
		},
	}
}

func runWith(args []string, stdout io.Writer, deps dependencies) error {
	if len(args) == 0 {
		return errors.New("usage: livecanary manifest -snapshot <snapshot.json> -out <manifest.json> | livecanary fetch -out <manifest.json>")
	}
	switch args[0] {
	case "manifest":
		return runManifest(args[1:], stdout, deps)
	case "fetch":
		return runFetch(args[1:], stdout, deps)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runManifest(args []string, stdout io.Writer, deps dependencies) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	snapshotPath := flags.String("snapshot", "", "registry snapshot descriptor")
	outPath := flags.String("out", "", "manifest output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *snapshotPath == "" || *outPath == "" {
		return errors.New("manifest: -snapshot and -out are required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("manifest: unexpected arguments: %v", flags.Args())
	}

	snapshot, err := deps.loadSnapshot(*snapshotPath)
	if err != nil {
		return err
	}
	provenance, err := deps.detect()
	if err != nil {
		return err
	}
	result, err := livecanary.Build(livecanary.BuildInput{
		Snapshot:   snapshot.Snapshot,
		Raw:        snapshot.Raw,
		Provenance: provenance,
		ChainID:    livecanary.ChainID,
	})
	if err != nil {
		return err
	}
	manifestBytes, err := writeArtifacts(*outPath, result.Manifest, result.Artifact)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "manifest=%s\nmanifest_sha256=%s\nregistry_status=%s\n",
		*outPath, livecanary.ManifestSHA256(manifestBytes), result.Manifest.Registry.Status); err != nil {
		return err
	}
	return nil
}

func runFetch(args []string, stdout io.Writer, deps dependencies) error {
	flags := flag.NewFlagSet("fetch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outPath := flags.String("out", "", "manifest output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return errors.New("fetch: -out is required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("fetch: unexpected arguments: %v", flags.Args())
	}

	provenance, err := deps.detect()
	if err != nil {
		return err
	}
	newFetcher := deps.newFetcher
	if newFetcher == nil {
		newFetcher = func() livecanary.Fetcher { return livecanary.Fetcher{} }
	}
	result, err := newFetcher().Acquire(context.Background(), provenance)
	if err != nil {
		return err
	}
	manifestBytes, err := writeArtifacts(*outPath, result.Manifest, result.Artifact)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "manifest=%s\nmanifest_sha256=%s\nregistry_status=%s\nmembership=%s\n",
		*outPath, livecanary.ManifestSHA256(manifestBytes), result.Manifest.Registry.Status, result.Membership); err != nil {
		return err
	}
	if result.Inconclusive() {
		if _, err := fmt.Fprintln(stdout, "state=inconclusive"); err != nil {
			return err
		}
		return &inconclusiveError{reason: string(result.Membership)}
	}
	return nil
}

func writeArtifacts(outPath string, manifest livecanary.Manifest, artifact []byte) ([]byte, error) {
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		return nil, err
	}
	outDir := filepath.Dir(outPath)
	if len(artifact) > 0 {
		artifactPath := filepath.Join(outDir, livecanary.ArtifactName)
		if err := writeFileAtomic(artifactPath, artifact); err != nil {
			return nil, err
		}
	}
	if err := writeFileAtomic(outPath, manifestBytes); err != nil {
		return nil, err
	}
	return manifestBytes, nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := file.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(name)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	renamed = true
	return nil
}
