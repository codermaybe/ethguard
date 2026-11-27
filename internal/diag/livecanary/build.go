package livecanary

import (
	"fmt"
	"slices"
)

type BuildInput struct {
	Snapshot   Snapshot
	Raw        []byte
	Provenance Provenance
	ChainID    uint64
}

type BuildResult struct {
	Manifest         Manifest
	Artifact         []byte
	RegistryError    error
	TargetChainFound bool
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
	return BuildRegistry(input.Snapshot.FetchedAt, input.Raw, input.Provenance)
}

// BuildRegistry builds the manifest from registry bytes fetched by the live
// path, where no on-disk snapshot descriptor exists. The source URL is fixed.
func BuildRegistry(fetchedAt string, raw []byte, provenance Provenance) (BuildResult, error) {
	if err := validateTimestamp("registry.fetched_at", fetchedAt); err != nil {
		return BuildResult{}, err
	}
	return buildFromRegistry(fetchedAt, raw, provenance)
}

// BuildUnavailable records a fetch failure as a valid pre-plan manifest. It
// never produces an artifact.
func BuildUnavailable(fetchedAt string, provenance Provenance) (BuildResult, error) {
	if err := validateTimestamp("registry.fetched_at", fetchedAt); err != nil {
		return BuildResult{}, err
	}
	manifest, err := baseManifest(provenance)
	if err != nil {
		return BuildResult{}, err
	}
	manifest.Registry = unavailableRegistry(fetchedAt)
	if err := manifest.Validate(); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Manifest: manifest}, nil
}

func buildFromRegistry(fetchedAt string, raw []byte, provenance Provenance) (BuildResult, error) {
	manifest, err := baseManifest(provenance)
	if err != nil {
		return BuildResult{}, err
	}
	result := BuildResult{}
	selection, found, selectionErr := SelectWithTarget(ChainID, raw)
	result.TargetChainFound = found
	if selectionErr != nil {
		manifest.Registry = unavailableRegistry(fetchedAt)
		manifest.Selection = nil
		result.RegistryError = selectionErr
	} else {
		artifact, mode, err := redactRegistry(raw)
		if err != nil {
			return BuildResult{}, err
		}
		if err := verifyRedaction(selection, artifact, mode); err != nil {
			return BuildResult{}, err
		}
		rawSHA := sha256Hex(raw)
		artifactSHA := sha256Hex(artifact)
		artifactName := ArtifactName
		manifest.Registry = Registry{
			Status:         "ok",
			URL:            ChainlistURL,
			FetchedAt:      fetchedAt,
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

func baseManifest(provenance Provenance) (Manifest, error) {
	generatedAt, err := formatProvenanceTime("generated_at", provenance.GeneratedAt)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
		SchemaVersion: SchemaVersion,
		RunID:         provenance.RunID,
		GeneratedAt:   generatedAt,
		SourceCommit:  provenance.SourceCommit,
		ToolVersion:   ToolVersion,
		GoVersion:     provenance.GoVersion,
		Environment:   provenance.Environment,
		ChainID:       ChainID,
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
	}, nil
}

func unavailableRegistry(fetchedAt string) Registry {
	return Registry{
		Status:    "unavailable",
		URL:       ChainlistURL,
		FetchedAt: fetchedAt,
		Redaction: RedactionNone,
	}
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
