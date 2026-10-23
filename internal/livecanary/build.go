package livecanary

import (
	"fmt"
	"slices"
	"time"
)

type BuildInput struct {
	Snapshot   Snapshot
	Raw        []byte
	Provenance Provenance
	ChainID    uint64
}

type BuildResult struct {
	Manifest      Manifest
	Artifact      []byte
	RegistryError error
}

// Build derives the manifest from registry bytes, a verified snapshot
// descriptor, and runner provenance. A registry that cannot be parsed yields a
// valid manifest with registry.status=unavailable and no selection; missing or
// inconsistent provenance is an error.
func Build(input BuildInput) (BuildResult, error) {
	if input.ChainID != ChainID {
		return BuildResult{}, fmt.Errorf("chain_id: got %d, want %d", input.ChainID, ChainID)
	}
	if err := input.Snapshot.Validate(); err != nil {
		return BuildResult{}, err
	}
	if actual := sha256Hex(input.Raw); actual != input.Snapshot.RawSHA256 {
		return BuildResult{}, fmt.Errorf("snapshot: raw_sha256 mismatch: got %s, want %s", actual, input.Snapshot.RawSHA256)
	}

	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		RunID:         input.Provenance.RunID,
		GeneratedAt:   input.Provenance.GeneratedAt.UTC().Truncate(time.Second).Format(time.RFC3339),
		SourceCommit:  input.Provenance.SourceCommit,
		ToolVersion:   ToolVersion,
		GoVersion:     input.Provenance.GoVersion,
		Environment:   input.Provenance.Environment,
		ChainID:       input.ChainID,
		Reference: Reference{
			Policy:       ReferencePolicy,
			MaxLagBlocks: MaxLagBlocks,
		},
		Timeouts: Timeouts{
			RunMS:       RunTimeoutMS,
			RegistryMS:  RegistryTimeoutMS,
			ReferenceMS: ReferenceTimeoutMS,
			StepMS:      StepTimeoutMS,
		},
		Limits: Limits{RegistryMaxBytes: RegistryMaxBytes},
		Plan:   FixedPlan(),
	}

	result := BuildResult{}
	selection, selectionErr := Select(input.ChainID, input.Raw)
	if selectionErr != nil {
		manifest.Registry = Registry{
			Status:    "unavailable",
			URL:       input.Snapshot.URL,
			FetchedAt: input.Snapshot.FetchedAt,
			Redaction: RedactionNone,
		}
		manifest.Selection = nil
		result.RegistryError = selectionErr
	} else {
		artifact, mode, err := redactRegistry(input.Raw)
		if err != nil {
			return BuildResult{}, err
		}
		if err := verifyRedaction(selection, artifact, mode); err != nil {
			return BuildResult{}, err
		}
		rawSHA := sha256Hex(input.Raw)
		artifactSHA := sha256Hex(artifact)
		artifactName := ArtifactName
		manifest.Registry = Registry{
			Status:         "ok",
			URL:            input.Snapshot.URL,
			FetchedAt:      input.Snapshot.FetchedAt,
			RawSHA256:      &rawSHA,
			Artifact:       &artifactName,
			ArtifactSHA256: &artifactSHA,
			Redaction:      mode,
		}
		manifest.Selection = selection
		result.Artifact = artifact
	}

	if err := manifest.Validate(); err != nil {
		return BuildResult{}, err
	}
	result.Manifest = manifest
	return result, nil
}

// verifyRedaction proves that publishing the safe copy cannot change which
// members are selected or why candidates were filtered, and that no
// credential-bearing value survived the copy.
func verifyRedaction(original *Selection, artifact []byte, mode string) error {
	_, again, err := redactRegistry(artifact)
	if err != nil {
		return fmt.Errorf("registry: verify redacted copy: %w", err)
	}
	if mode == RedactionURLCredentials && again != RedactionNone {
		return fmt.Errorf("registry: redacted copy still contains credential-bearing values")
	}
	replayed, err := Select(ChainID, artifact)
	if err != nil {
		return fmt.Errorf("registry: redacted copy is not selectable: %w", err)
	}
	if replayed.CandidateCount != original.CandidateCount ||
		!slices.Equal(replayed.Selected, original.Selected) ||
		len(replayed.Filtered) != len(original.Filtered) {
		return fmt.Errorf("registry: redaction changed selected members or candidate count")
	}
	for i := range original.Filtered {
		if replayed.Filtered[i].Reason != original.Filtered[i].Reason {
			return fmt.Errorf("registry: redaction changed filter reason at index %d", i)
		}
	}
	return nil
}
