package livecanary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/pool"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func TestSelectFiltersAndSorts(t *testing.T) {
	raw := readTestdata(t, "registry.json")
	selection, err := Select(ChainID, raw)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if err := selection.validate(); err != nil {
		t.Fatalf("selection validate: %v", err)
	}
	if selection.CandidateCount != 13 {
		t.Fatalf("candidate_count = %d, want 13", selection.CandidateCount)
	}
	wantSelected := []SelectedMember{
		{SourceID: "node-01", URL: "https://alpha.example"},
		{SourceID: "node-02", URL: "https://beta.example/rpc"},
		{SourceID: "node-03", URL: "https://delta.example"},
		{SourceID: "node-04", URL: "https://epsilon.example"},
		{SourceID: "node-05", URL: "https://zeta.example/rpc"},
	}
	if len(selection.Selected) != len(wantSelected) {
		t.Fatalf("selected = %d members, want %d", len(selection.Selected), len(wantSelected))
	}
	for i, want := range wantSelected {
		if selection.Selected[i] != want {
			t.Errorf("selected[%d] = %+v, want %+v", i, selection.Selected[i], want)
		}
	}
	wantReasons := []string{
		filterNonHTTPS,
		filterCredentials,
		filterCredentials,
		filterDuplicate,
		filterInvalidURL,
		filterInvalidURL,
		filterInvalidURL,
		filterCredentials,
	}
	if len(selection.Filtered) != len(wantReasons) {
		t.Fatalf("filtered = %d entries, want %d", len(selection.Filtered), len(wantReasons))
	}
	for i, want := range wantReasons {
		if selection.Filtered[i].Reason != want {
			t.Errorf("filtered[%d].reason = %q, want %q", i, selection.Filtered[i].Reason, want)
		}
	}

	wantHashes := []string{
		"90560029490f8688e97e6118798cf4ac375ae5a220773e85af0ddef902b8f9b3",
		"1594839780d4e30f7a766c085e16e138ac2505ec082464ae2585cd1e6953d84a",
		"af1d7fb111bf1577651a151ce89f9dd18103d4036fb794173f07938909b0a50e",
		"ef3c9d793898315445ee9750d0c3b77b0e84b9dffdd4d7710f470729b5dad1bb",
		"911c4ce028643aa6a377ac1bb187319a0f4b3e95b6472c3412a2fb6a39c9a6b6",
		"", // filled below from the raw entry with no URL
		"d8b5bf9b9fd4760c61234d12614d80c96892c75fe92d8819c6320f1ca6b3533d",
		"77317942f2da47dfac89a9ce1b53238427b2a0fd482386ee4ccd87fbb2dca572",
	}
	var chains []struct {
		ChainID json.RawMessage   `json:"chainId"`
		RPC     []json.RawMessage `json:"rpc"`
	}
	if err := json.Unmarshal(raw, &chains); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	wantHashes[5] = sha256Hex(chains[0].RPC[8])
	for i, want := range wantHashes {
		if selection.Filtered[i].URLSHA256 != want {
			t.Errorf("filtered[%d].url_sha256 = %s, want %s", i, selection.Filtered[i].URLSHA256, want)
		}
	}
	wantBasis := "7d4e8bfda2581aeed9e6e21a7a0e88e9d60c2951a2ee1d46a9160aff6df3ecdd"
	if selection.VotingBasisSHA256 != wantBasis {
		t.Errorf("voting_basis_sha256 = %s, want %s", selection.VotingBasisSHA256, wantBasis)
	}
	for _, member := range selection.Selected {
		if strings.Contains(member.URL, "SECRET") || strings.Contains(member.URL, "secret") {
			t.Errorf("selected URL leaked credentials: %s", member.URL)
		}
	}
}

func TestSelectCapExceeded(t *testing.T) {
	rpc := make([]map[string]string, 0, MaxMembers+3)
	for i := 0; i < MaxMembers+3; i++ {
		rpc = append(rpc, map[string]string{"url": fmt.Sprintf("https://node-%02d.example", i)})
	}
	raw, err := json.Marshal([]any{map[string]any{"chainId": ChainID, "rpc": rpc}})
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	selection, err := Select(ChainID, raw)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(selection.Selected) != MaxMembers {
		t.Fatalf("selected = %d, want %d", len(selection.Selected), MaxMembers)
	}
	if len(selection.Filtered) != 3 {
		t.Fatalf("filtered = %d, want 3", len(selection.Filtered))
	}
	for _, entry := range selection.Filtered {
		if entry.Reason != filterCapExceeded {
			t.Errorf("filter reason = %q, want %q", entry.Reason, filterCapExceeded)
		}
	}
	if selection.Selected[0].URL != "https://node-00.example" ||
		selection.Selected[MaxMembers-1].URL != "https://node-11.example" {
		t.Errorf("unexpected cap boundary: first=%s last=%s",
			selection.Selected[0].URL, selection.Selected[MaxMembers-1].URL)
	}
}

func TestSelectMissingTargetChain(t *testing.T) {
	selection, err := Select(ChainID, []byte(`[{"chainId":2,"rpc":["https://other.example"]}]`))
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if selection.CandidateCount != 0 || len(selection.Selected) != 0 || len(selection.Filtered) != 0 {
		t.Fatalf("expected empty selection, got %+v", selection)
	}
	if selection.VotingBasisSHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("empty voting basis = %s", selection.VotingBasisSHA256)
	}
	if err := selection.validate(); err != nil {
		t.Errorf("empty selection should be structurally valid: %v", err)
	}
}

func TestSelectWithTargetReportsPresence(t *testing.T) {
	selection, found, err := SelectWithTarget(ChainID, []byte(`[{"chainId":2,"rpc":["https://other.example"]}]`))
	if err != nil {
		t.Fatalf("SelectWithTarget: %v", err)
	}
	if found {
		t.Error("missing target chain reported as present")
	}
	if selection.CandidateCount != 0 {
		t.Errorf("candidate count = %d", selection.CandidateCount)
	}
	selection, found, err = SelectWithTarget(ChainID, []byte(`[{"chainId":1,"rpc":[]}]`))
	if err != nil {
		t.Fatalf("SelectWithTarget: %v", err)
	}
	if !found {
		t.Error("present target chain reported as missing")
	}
	if selection.CandidateCount != 0 {
		t.Errorf("candidate count = %d", selection.CandidateCount)
	}
}

func TestSelectRejectsMalformedRegistry(t *testing.T) {
	for _, raw := range []string{
		"",
		"null",
		"{}",
		`[{"chainId":1}]`,
		`[{"chainId":1,"rpc":"https://x.example"}]`,
	} {
		if _, err := Select(ChainID, []byte(raw)); err == nil {
			t.Errorf("Select(%q) succeeded, want error", raw)
		}
	}
}

func TestSelectToleratesOtherChainProblems(t *testing.T) {
	raw := []byte(`[
		{"chainId":2,"rpc":[{"bogus":true},123]},
		{"chainId":"broken","rpc":{"not":"a list"}},
		{"chainId":1,"rpc":["https://only.example"]}
	]`)
	selection, err := Select(ChainID, raw)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if selection.CandidateCount != 1 || len(selection.Selected) != 1 {
		t.Fatalf("unexpected selection: %+v", selection)
	}
}

func TestEndpointIdentityMatchesPool(t *testing.T) {
	urls := []string{
		"https://Example.COM/",
		"https://example.com:443/rpc",
		"https://example.com:8443/rpc",
		"http://example.com:80",
		"ws://Example.com:80/ws",
		"wss://example.com:443",
		"https://[2001:db8::1]:443/rpc",
		"https://example.com/rpc?b=2&a=1",
	}
	registry := pool.NewPool()
	defer registry.Close()
	for i, raw := range urls {
		want, err := EndpointIdentity(raw)
		if err != nil {
			t.Fatalf("EndpointIdentity(%q): %v", raw, err)
		}
		source, err := registry.Join(fmt.Sprintf("node-%02d", i), raw)
		if err != nil {
			t.Fatalf("pool.Join(%q): %v", raw, err)
		}
		if source.ID() != want {
			t.Errorf("identity drift for %q: pool=%q livecanary=%q", raw, source.ID(), want)
		}
	}
}

func TestSelectionReproducibleOnRedactedArtifact(t *testing.T) {
	raw := readTestdata(t, "registry.json")
	original, err := Select(ChainID, raw)
	if err != nil {
		t.Fatalf("Select raw: %v", err)
	}
	artifact, mode, err := redactRegistry(raw)
	if err != nil {
		t.Fatalf("redactRegistry: %v", err)
	}
	if mode != RedactionURLCredentials {
		t.Fatalf("redaction mode = %q, want %q", mode, RedactionURLCredentials)
	}
	replayed, err := Select(ChainID, artifact)
	if err != nil {
		t.Fatalf("Select artifact: %v", err)
	}
	if replayed.CandidateCount != original.CandidateCount {
		t.Errorf("candidate count changed: %d -> %d", original.CandidateCount, replayed.CandidateCount)
	}
	if !slices.Equal(replayed.Selected, original.Selected) {
		t.Errorf("selected changed after redaction:\nraw: %+v\nartifact: %+v", original.Selected, replayed.Selected)
	}
	if len(replayed.Filtered) != len(original.Filtered) {
		t.Fatalf("filtered count changed: %d -> %d", len(original.Filtered), len(replayed.Filtered))
	}
	for i := range original.Filtered {
		if replayed.Filtered[i].Reason != original.Filtered[i].Reason {
			t.Errorf("filtered[%d].reason changed: %s -> %s",
				i, original.Filtered[i].Reason, replayed.Filtered[i].Reason)
		}
	}
}
