package livecanary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLiveCanaryWorkflowContract(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "live-canary.yml")
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
	if _, exists := triggers["pull_request"]; exists {
		t.Fatal("live canary must not run on pull_request")
	}
	if _, exists := triggers["schedule"]; !exists {
		t.Fatal("workflow has no schedule trigger")
	}
	push, ok := triggers["push"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no push trigger")
	}
	tags, ok := push["tags"].([]any)
	if !ok || len(tags) == 0 || tags[0] != "v*" {
		t.Fatalf("push tags = %v, want v*", push["tags"])
	}
	if _, exists := triggers["workflow_dispatch"]; !exists {
		t.Fatal("workflow has no workflow_dispatch trigger")
	}
	permissions, ok := workflow["permissions"].(map[string]any)
	if !ok || permissions["contents"] != "read" {
		t.Fatalf("permissions = %v, want contents: read", workflow["permissions"])
	}
	jobs, ok := workflow["jobs"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no jobs")
	}
	job, ok := jobs["live-canary"].(map[string]any)
	if !ok {
		t.Fatal("workflow has no live-canary job")
	}
	if minutes, ok := job["timeout-minutes"].(int); !ok || minutes < 10 {
		t.Fatalf("timeout-minutes = %v, want at least 10", job["timeout-minutes"])
	}
	steps, ok := job["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatal("live-canary job has no steps")
	}
	var uses, runs strings.Builder
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("step is not an object: %v", raw)
		}
		if action, ok := step["uses"].(string); ok {
			uses.WriteString(action + "\n")
		}
		if command, ok := step["run"].(string); ok {
			runs.WriteString(command + "\n")
		}
	}
	if !strings.Contains(uses.String(), "actions/upload-artifact@v4") {
		t.Error("workflow does not upload an artifact")
	}
	if !strings.Contains(runs.String(), `"${RUNNER_TEMP}/livecanary" run`) {
		t.Error("workflow does not run the live canary")
	}
	if !strings.Contains(runs.String(), `"${RUNNER_TEMP}/livecanary" verify -dir`) {
		t.Error("workflow does not verify artifact coherence")
	}
	if !strings.Contains(string(data), "ref: ${{ github.sha }}") {
		t.Error("workflow does not pin checkout to the exact commit")
	}
	if !strings.Contains(string(data), "retention-days: 90") {
		t.Error("workflow does not declare artifact retention")
	}
	if !strings.Contains(string(data), "if-no-files-found: error") {
		t.Error("workflow does not fail on missing artifacts")
	}
	if !strings.Contains(string(data), "continue-on-error: true") ||
		!strings.Contains(string(data), "steps.canary.outcome != 'success'") {
		t.Error("workflow does not surface a non-passed observation as a failure")
	}
}
