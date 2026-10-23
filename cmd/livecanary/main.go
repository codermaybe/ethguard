// Command livecanary generates the T10 live-canary provenance manifest from a
// verified registry snapshot. It never contacts a network source.
package main

import (
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
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "livecanary: %v\n", err)
		os.Exit(1)
	}
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
	}
}

func runWith(args []string, stdout io.Writer, deps dependencies) error {
	if len(args) == 0 {
		return errors.New("usage: livecanary manifest -snapshot <snapshot.json> -out <manifest.json>")
	}
	switch args[0] {
	case "manifest":
		return runManifest(args[1:], stdout, deps)
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
	manifestBytes, err := result.Manifest.Marshal()
	if err != nil {
		return err
	}

	outDir := filepath.Dir(*outPath)
	if len(result.Artifact) > 0 {
		artifactPath := filepath.Join(outDir, livecanary.ArtifactName)
		if err := writeFileAtomic(artifactPath, result.Artifact); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(*outPath, manifestBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "manifest=%s\nmanifest_sha256=%s\nregistry_status=%s\n",
		*outPath, livecanary.ManifestSHA256(manifestBytes), result.Manifest.Registry.Status); err != nil {
		return err
	}
	return nil
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
