package livecanary

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

type Provenance struct {
	Environment  Environment
	SourceCommit string
	GoVersion    string
	RunID        string
	GeneratedAt  time.Time
}

// Detector derives provenance from the runner. No field is accepted from a
// command-line flag: a manifest must not be assembled by hand.
type Detector struct {
	Getenv  func(string) string
	GitHead func() (string, error)
	Now     func() time.Time
	Random  io.Reader
}

func (d Detector) Detect() (Provenance, error) {
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	random := d.Random
	if random == nil {
		random = rand.Reader
	}

	environment, err := detectEnvironment(getenv)
	if err != nil {
		return Provenance{}, err
	}

	var commit string
	if environment.Kind == "ci" {
		commit = getenv("GITHUB_SHA")
	} else {
		gitHead := d.GitHead
		if gitHead == nil {
			gitHead = defaultGitHead
		}
		commit, err = gitHead()
		if err != nil {
			return Provenance{}, fmt.Errorf("source_commit: %w", err)
		}
	}
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !sourceCommitRE.MatchString(commit) {
		return Provenance{}, fmt.Errorf("source_commit: %q is not a 40-character git SHA", commit)
	}

	generatedAt := now().UTC().Truncate(time.Second)
	runID, err := newRunID(generatedAt, random)
	if err != nil {
		return Provenance{}, err
	}
	return Provenance{
		Environment:  environment,
		SourceCommit: commit,
		GoVersion:    runtime.Version(),
		RunID:        runID,
		GeneratedAt:  generatedAt,
	}, nil
}

func detectEnvironment(getenv func(string) string) (Environment, error) {
	ci := getenv("CI")
	githubActions := getenv("GITHUB_ACTIONS")
	if githubActions != "true" && ci != "true" && ci != "1" {
		return Environment{Kind: "local", RunnerClass: "local"}, nil
	}
	if githubActions != "true" {
		return Environment{}, fmt.Errorf("environment: unsupported CI provider")
	}
	runnerClass := "self-hosted"
	if getenv("RUNNER_ENVIRONMENT") == "github-hosted" {
		runnerClass = "github-hosted"
	}
	required := make(map[string]string, 5)
	for _, key := range []string{"GITHUB_REPOSITORY", "GITHUB_WORKFLOW_REF", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_JOB"} {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return Environment{}, fmt.Errorf("environment: %s is required on GitHub Actions", key)
		}
		required[key] = value
	}
	return Environment{
		Kind:        "ci",
		RunnerClass: runnerClass,
		CI: &CIEnvironment{
			Provider:    "github-actions",
			Repository:  required["GITHUB_REPOSITORY"],
			WorkflowRef: required["GITHUB_WORKFLOW_REF"],
			RunID:       required["GITHUB_RUN_ID"],
			RunAttempt:  required["GITHUB_RUN_ATTEMPT"],
			Job:         required["GITHUB_JOB"],
		},
	}, nil
}

func defaultGitHead() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// newRunID produces a ULID-shaped identifier: 48-bit timestamp followed by
// 80 random bits, encoded in Crockford base32.
func newRunID(now time.Time, random io.Reader) (string, error) {
	var raw [16]byte
	millis := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(millis)
		millis >>= 8
	}
	if _, err := io.ReadFull(random, raw[6:]); err != nil {
		return "", fmt.Errorf("run_id: read randomness: %w", err)
	}
	value := new(big.Int).SetBytes(raw[:])
	base := big.NewInt(32)
	id := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(value, base, remainder)
		id[i] = ulidAlphabet[remainder.Int64()]
		value = quotient
	}
	return string(id), nil
}
