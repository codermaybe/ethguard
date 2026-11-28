package livecanary

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLiveCanaryRecheckChecksExecutionBeforeSourceCheckout(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "live-canary-recheck.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run             string `yaml:"run"`
				If              string `yaml:"if"`
				ContinueOnError bool   `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var gate string
	for _, job := range workflow.Jobs {
		build, check, checkout := -1, -1, -1
		for index, step := range job.Steps {
			switch {
			case strings.Contains(step.Run, `-o "${RUNNER_TEMP}/livecanary-current"`):
				build = index
			case strings.Contains(step.Run, `"${RUNNER_TEMP}/livecanary-current" check-rerun`):
				check, gate = index, step.Run
				if step.If != "" || step.ContinueOnError {
					t.Fatal("execution provenance gate must be unconditional and stop on failure")
				}
			case strings.Contains(step.Run, "git checkout --detach"):
				checkout = index
				if step.If != "" || step.ContinueOnError {
					t.Fatal("source checkout must depend on successful preceding steps")
				}
			}
		}
		if checkout >= 0 && (build < 0 || check <= build || check >= checkout) {
			t.Fatalf("current build/check/checkout order = %d/%d/%d", build, check, checkout)
		}
	}
	if gate == "" {
		t.Fatal("workflow does not check provenance with the current binary")
	}
	if strings.Contains(string(data), ".http_adapter") || strings.Contains(string(data), "source_adapter") {
		t.Fatal("workflow duplicates manifest provenance policy")
	}
	for _, status := range []string{"0", "23"} {
		t.Run("exit "+status, func(t *testing.T) {
			dir := t.TempDir()
			stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"${RUNNER_TEMP}/gate-args\"\nexit \"${GATE_EXIT_CODE}\"\n"
			if err := os.WriteFile(filepath.Join(dir, "livecanary-current"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-c", gate+"\nprintf 'reached\\n' > \"${RUNNER_TEMP}/after-gate\"\n")
			cmd.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "SOURCE_DIR="+dir, "GATE_EXIT_CODE="+status)
			output, runErr := cmd.CombinedOutput()
			if (runErr == nil) != (status == "0") {
				t.Fatalf("gate status=%s, shell error=%v, output=%s", status, runErr, output)
			}
			args, err := os.ReadFile(filepath.Join(dir, "gate-args"))
			if err != nil {
				t.Fatal(err)
			}
			if want := "check-rerun\n-manifest\n" + filepath.Join(dir, "manifest.json") + "\n"; string(args) != want {
				t.Fatalf("gate arguments = %q, want %q", args, want)
			}
			_, markerErr := os.Stat(filepath.Join(dir, "after-gate"))
			if (markerErr == nil) != (status == "0") {
				t.Fatalf("gate status=%s did not control subsequent execution: %v", status, markerErr)
			}
		})
	}
}

// uploadArtifactAction deliberately omits the major version: Dependabot owns
// the version, while the contract only requires that an artifact is uploaded.
const uploadArtifactAction = "actions/upload-artifact@v"

func TestLiveCanaryRecheckWorkflowContract(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".github", "workflows", "live-canary-recheck.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var workflow map[string]any
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	triggers, ok := workflow["on"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no on block")
	}
	for _, forbidden := range []string{"pull_request", "push", "schedule"} {
		if _, exists := triggers[forbidden]; exists {
			t.Fatalf("recheck workflow must not run on %s", forbidden)
		}
	}
	dispatch, ok := triggers["workflow_dispatch"].(map[string]any)
	if !ok {
		t.Fatal("recheck workflow has no workflow_dispatch trigger")
	}
	inputs, ok := dispatch["inputs"].(map[string]any)
	if !ok {
		t.Fatal("recheck workflow has no inputs")
	}
	for _, required := range []string{"source_run_id", "source_artifact"} {
		input, ok := inputs[required].(map[string]any)
		if !ok || input["required"] != true {
			t.Fatalf("input %q is not required", required)
		}
	}
	if _, ok := inputs["expected_manifest_sha256"]; !ok {
		t.Fatal("recheck workflow has no expected_manifest_sha256 input")
	}
	permissions, ok := workflow["permissions"].(map[string]any)
	if !ok || permissions["contents"] != "read" || permissions["actions"] != "read" {
		t.Fatalf("permissions = %v, want contents/actions read", workflow["permissions"])
	}
	text := string(data)
	for _, want := range []string{
		"gh run download",
		"sha256sum -c SHA256SUMS",
		"git checkout --detach",
		`"${RUNNER_TEMP}/livecanary-source" rerun`,
		`"${RUNNER_TEMP}/livecanary-current" compare`,
		uploadArtifactAction,
		"retention-days: 90",
		"steps.compare.outcome != 'success'",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("recheck workflow does not contain %q", want)
		}
	}
}
