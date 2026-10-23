package livecanary

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 10, 23, 12, 0, 0, 500_000_000, time.UTC)
}

func envLookup(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestDetectLocalProvenance(t *testing.T) {
	detector := Detector{
		Getenv:  envLookup(nil),
		GitHead: func() (string, error) { return strings.Repeat("C", 40), nil },
		Now:     fixedNow,
		Random:  bytes.NewReader(make([]byte, 10)),
	}
	provenance, err := detector.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if provenance.Environment.Kind != "local" || provenance.Environment.RunnerClass != "local" {
		t.Errorf("environment = %+v", provenance.Environment)
	}
	if provenance.Environment.CI != nil {
		t.Error("local provenance must not carry CI identity")
	}
	if provenance.SourceCommit != strings.Repeat("c", 40) {
		t.Errorf("source commit = %q, want lowercase", provenance.SourceCommit)
	}
	if provenance.GoVersion == "" {
		t.Error("go_version is empty")
	}
	if provenance.RunID != "01M5MA3PG00000000000000000" {
		t.Errorf("run id = %s", provenance.RunID)
	}
	if provenance.GeneratedAt.Nanosecond() != 0 {
		t.Errorf("generated_at is not truncated: %v", provenance.GeneratedAt)
	}
}

func TestDetectCIProvenance(t *testing.T) {
	values := map[string]string{
		"CI":                  "true",
		"GITHUB_ACTIONS":      "true",
		"RUNNER_ENVIRONMENT":  "github-hosted",
		"GITHUB_REPOSITORY":   "codermaybe/ethguard",
		"GITHUB_WORKFLOW_REF": "codermaybe/ethguard/.github/workflows/live-canary.yml@refs/heads/master",
		"GITHUB_RUN_ID":       "12345678901",
		"GITHUB_RUN_ATTEMPT":  "2",
		"GITHUB_JOB":          "live-canary",
		"GITHUB_SHA":          strings.Repeat("B", 40),
	}
	detector := Detector{
		Getenv: envLookup(values),
		GitHead: func() (string, error) {
			return "", errors.New("git must not be consulted in CI")
		},
		Now:    fixedNow,
		Random: bytes.NewReader(make([]byte, 10)),
	}
	provenance, err := detector.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	environment := provenance.Environment
	if environment.Kind != "ci" || environment.RunnerClass != "github-hosted" {
		t.Errorf("environment = %+v", environment)
	}
	if environment.CI == nil {
		t.Fatal("CI identity is nil")
	}
	if environment.CI.Provider != "github-actions" ||
		environment.CI.Repository != "codermaybe/ethguard" ||
		environment.CI.RunID != "12345678901" ||
		environment.CI.RunAttempt != "2" ||
		environment.CI.Job != "live-canary" {
		t.Errorf("CI identity = %+v", environment.CI)
	}
	if provenance.SourceCommit != strings.Repeat("b", 40) {
		t.Errorf("source commit = %q", provenance.SourceCommit)
	}
}

func TestDetectSelfHostedRunner(t *testing.T) {
	values := map[string]string{
		"CI":                  "true",
		"GITHUB_ACTIONS":      "true",
		"GITHUB_REPOSITORY":   "codermaybe/ethguard",
		"GITHUB_WORKFLOW_REF": "workflow",
		"GITHUB_RUN_ID":       "1",
		"GITHUB_RUN_ATTEMPT":  "1",
		"GITHUB_JOB":          "job",
		"GITHUB_SHA":          strings.Repeat("a", 40),
	}
	provenance, err := (Detector{
		Getenv: envLookup(values),
		Now:    fixedNow,
		Random: bytes.NewReader(make([]byte, 10)),
	}).Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if provenance.Environment.RunnerClass != "self-hosted" {
		t.Errorf("runner class = %q", provenance.Environment.RunnerClass)
	}
}

func TestDetectRejectsMissingProvenance(t *testing.T) {
	base := map[string]string{
		"CI":                  "true",
		"GITHUB_ACTIONS":      "true",
		"GITHUB_REPOSITORY":   "codermaybe/ethguard",
		"GITHUB_WORKFLOW_REF": "workflow",
		"GITHUB_RUN_ID":       "1",
		"GITHUB_RUN_ATTEMPT":  "1",
		"GITHUB_JOB":          "job",
		"GITHUB_SHA":          strings.Repeat("a", 40),
	}
	tests := []struct {
		name   string
		mutate func(map[string]string)
		random *bytes.Reader
		git    func() (string, error)
	}{
		{"unsupported ci", func(values map[string]string) { delete(values, "GITHUB_ACTIONS") }, nil, nil},
		{"missing job", func(values map[string]string) { delete(values, "GITHUB_JOB") }, nil, nil},
		{"short sha", func(values map[string]string) { values["GITHUB_SHA"] = "abc" }, nil, nil},
		{"missing random", func(map[string]string) {}, bytes.NewReader([]byte{1, 2}), nil},
		{
			"git failure", func(values map[string]string) {
				delete(values, "CI")
				delete(values, "GITHUB_ACTIONS")
			}, nil, func() (string, error) { return "", errors.New("no repository") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				values[key] = value
			}
			test.mutate(values)
			random := test.random
			if random == nil {
				random = bytes.NewReader(make([]byte, 10))
			}
			git := test.git
			if git == nil {
				git = func() (string, error) { return strings.Repeat("a", 40), nil }
			}
			if _, err := (Detector{
				Getenv:  envLookup(values),
				GitHead: git,
				Now:     fixedNow,
				Random:  random,
			}).Detect(); err == nil {
				t.Fatal("Detect accepted missing or invalid provenance")
			}
		})
	}
}
