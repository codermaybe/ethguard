package livecanary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// uploadArtifactAction deliberately omits the major version: Dependabot owns
// the version, while the contract only requires that an artifact is uploaded.
const uploadArtifactAction = "actions/upload-artifact@v"

func TestLiveCanaryRecheckWorkflowContract(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "live-canary-recheck.yml")
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
