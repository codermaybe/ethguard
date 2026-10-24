// Command livecanary generates live-canary manifests and reports. The manifest
// subcommand is offline and consumes a verified snapshot descriptor; fetch
// performs one Chainlist acquisition and fixes the run's membership; run
// acquires once, executes the fixed plan, and writes manifest, registry
// artifact, and report. rerun executes the plan and membership fixed by an
// existing manifest without another registry acquisition. compare checks two
// observations of the same manifest. verify checks a published artifact
// directory offline. Exit code 2 means no conclusion (pre-plan failure or an
// inconclusive run); exit code 1 means the run failed or the tool itself
// failed.
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
	runPlan      func(context.Context, livecanary.RunInput) (livecanary.Report, error)
	rerunPlan    func(context.Context, []byte) (livecanary.Report, error)
}

type inconclusiveError struct {
	reason string
}

func (e *inconclusiveError) Error() string {
	return "inconclusive: " + e.reason
}

type failedError struct {
	reason string
}

func (e *failedError) Error() string {
	return "failed: " + e.reason
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
		return errors.New("usage: livecanary manifest -snapshot <snapshot.json> -out <manifest.json> | livecanary fetch -out <manifest.json> | livecanary run -out <manifest.json> | livecanary rerun -manifest <manifest.json> -out <report.json> | livecanary compare -manifest <manifest.json> -original <report.json> -recheck <report.json> -out <comparison.json> | livecanary verify -dir <artifact-dir>")
	}
	switch args[0] {
	case "manifest":
		return runManifest(args[1:], stdout, deps)
	case "fetch":
		return runFetch(args[1:], stdout, deps)
	case "run":
		return runLive(args[1:], stdout, deps)
	case "rerun":
		return runRerun(args[1:], stdout, deps)
	case "compare":
		return runCompare(args[1:], stdout)
	case "verify":
		return runVerify(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runCompare(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "manifest shared by both reports")
	originalPath := flags.String("original", "", "original report path")
	recheckPath := flags.String("recheck", "", "recheck report path")
	outPath := flags.String("out", "", "comparison output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *originalPath == "" || *recheckPath == "" || *outPath == "" {
		return errors.New("compare: -manifest, -original, -recheck, and -out are required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("compare: unexpected arguments: %v", flags.Args())
	}
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("compare: read manifest: %w", err)
	}
	originalBytes, err := os.ReadFile(*originalPath)
	if err != nil {
		return fmt.Errorf("compare: read original report: %w", err)
	}
	recheckBytes, err := os.ReadFile(*recheckPath)
	if err != nil {
		return fmt.Errorf("compare: read recheck report: %w", err)
	}
	comparison, err := livecanary.CompareReports(manifestBytes, originalBytes, recheckBytes)
	if err != nil {
		return err
	}
	comparisonBytes, err := comparison.Marshal()
	if err != nil {
		return err
	}
	if err := writeFileAtomic(*outPath, comparisonBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "comparison=%s\ncomparison_sha256=%s\noriginal_state=%s\nrecheck_state=%s\nrepeatable=%t\nclassification=%s\ndifferences=%d\n",
		*outPath, livecanary.ReportSHA256(comparisonBytes), comparison.OriginalState, comparison.RecheckState,
		comparison.Repeatable, comparison.Classification, len(comparison.Differences)); err != nil {
		return err
	}
	if !comparison.Repeatable {
		return &failedError{reason: "comparison " + comparison.Classification}
	}
	return nil
}

func runVerify(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", "", "artifact directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("verify: -dir is required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("verify: unexpected arguments: %v", flags.Args())
	}
	artifacts, err := livecanary.VerifyArtifacts(*dir)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "run_id=%s\nstate=%s\nmanifest_sha256=%s\nreport_sha256=%s\n",
		artifacts.RunID, artifacts.State, artifacts.ManifestSHA256, artifacts.ReportSHA256); err != nil {
		return err
	}
	if artifacts.RegistrySHA256 != "" {
		if _, err := fmt.Fprintf(stdout, "registry_sha256=%s\n", artifacts.RegistrySHA256); err != nil {
			return err
		}
	}
	return nil
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
	if err := writeArtifacts(*outPath, manifestBytes, result.Artifact); err != nil {
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
	manifestBytes, err := result.Manifest.Marshal()
	if err != nil {
		return err
	}
	if err := writeArtifacts(*outPath, manifestBytes, result.Artifact); err != nil {
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

func runLive(args []string, stdout io.Writer, deps dependencies) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outPath := flags.String("out", "", "manifest output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return errors.New("run: -out is required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("run: unexpected arguments: %v", flags.Args())
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
	manifestBytes, err := result.Manifest.Marshal()
	if err != nil {
		return err
	}
	if err := writeArtifacts(*outPath, manifestBytes, result.Artifact); err != nil {
		return err
	}
	input := livecanary.RunInput{
		Manifest:       result.Manifest,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: livecanary.ManifestSHA256(manifestBytes),
		Membership:     result.Membership,
	}
	runPlan := deps.runPlan
	if runPlan == nil {
		runPlan = livecanary.Executor{}.Run
	}
	report, err := runPlan(context.Background(), input)
	if err != nil {
		if _, printErr := fmt.Fprintf(stdout, "manifest=%s\nmanifest_sha256=%s\nregistry_status=%s\nmembership=%s\nreport_error=%v\n",
			*outPath, input.ManifestSHA256, result.Manifest.Registry.Status, result.Membership, err); printErr != nil {
			return printErr
		}
		return err
	}
	reportBytes, err := report.Marshal()
	if err != nil {
		return err
	}
	reportPath := filepath.Join(filepath.Dir(*outPath), livecanary.ReportArtifactName)
	if err := writeFileAtomic(reportPath, reportBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "manifest=%s\nmanifest_sha256=%s\nregistry_status=%s\nmembership=%s\nreport=%s\nreport_sha256=%s\nstate=%s\n",
		*outPath, input.ManifestSHA256, result.Manifest.Registry.Status, result.Membership,
		reportPath, livecanary.ReportSHA256(reportBytes), report.State); err != nil {
		return err
	}
	switch report.State {
	case livecanary.StatePassed:
		return nil
	case livecanary.StateInconclusive:
		return &inconclusiveError{reason: "run state inconclusive"}
	default:
		return &failedError{reason: "run state " + report.State}
	}
}

func runRerun(args []string, stdout io.Writer, deps dependencies) error {
	flags := flag.NewFlagSet("rerun", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "existing manifest path")
	outPath := flags.String("out", "", "report output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *outPath == "" {
		return errors.New("rerun: -manifest and -out are required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("rerun: unexpected arguments: %v", flags.Args())
	}
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("rerun: read manifest: %w", err)
	}
	rerunPlan := deps.rerunPlan
	if rerunPlan == nil {
		rerunPlan = func(ctx context.Context, bytes []byte) (livecanary.Report, error) {
			return livecanary.Rerun(ctx, bytes, livecanary.Executor{})
		}
	}
	report, err := rerunPlan(context.Background(), manifestBytes)
	if err != nil {
		return err
	}
	reportBytes, err := report.Marshal()
	if err != nil {
		return err
	}
	if err := writeFileAtomic(*outPath, reportBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "manifest=%s\nmanifest_sha256=%s\nreport=%s\nreport_sha256=%s\nstate=%s\n",
		*manifestPath, livecanary.ManifestSHA256(manifestBytes), *outPath,
		livecanary.ReportSHA256(reportBytes), report.State); err != nil {
		return err
	}
	switch report.State {
	case livecanary.StatePassed:
		return nil
	case livecanary.StateInconclusive:
		return &inconclusiveError{reason: "rerun state inconclusive"}
	default:
		return &failedError{reason: "rerun state " + report.State}
	}
}

func writeArtifacts(outPath string, manifestBytes, artifact []byte) error {
	outDir := filepath.Dir(outPath)
	if len(artifact) > 0 {
		artifactPath := filepath.Join(outDir, livecanary.ArtifactName)
		if err := writeFileAtomic(artifactPath, artifact); err != nil {
			return err
		}
	}
	return writeFileAtomic(outPath, manifestBytes)
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
