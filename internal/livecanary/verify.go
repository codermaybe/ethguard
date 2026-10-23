package livecanary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// VerifiedArtifacts is the result of checking one published artifact bundle.
// State is the observed run state, not an assertion that the observation
// passed; callers decide whether a non-passed state is acceptable.
type VerifiedArtifacts struct {
	RunID          string
	State          string
	ManifestSHA256 string
	ReportSHA256   string
	RegistrySHA256 string
}

// VerifyArtifacts checks that a directory contains a manifest, a report, and
// (when the registry was available) the published registry copy, and that the
// files bind to each other by digest and run identity. It performs no network
// access and does not rewrite any file.
func VerifyArtifacts(dir string) (VerifiedArtifacts, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(dir, ManifestArtifactName))
	if err != nil {
		return VerifiedArtifacts{}, fmt.Errorf("verify: read manifest: %w", err)
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return VerifiedArtifacts{}, fmt.Errorf("verify: %w", err)
	}
	reportBytes, err := os.ReadFile(filepath.Join(dir, ReportArtifactName))
	if err != nil {
		return VerifiedArtifacts{}, fmt.Errorf("verify: read report: %w", err)
	}
	report, err := ParseReport(reportBytes)
	if err != nil {
		return VerifiedArtifacts{}, fmt.Errorf("verify: %w", err)
	}
	manifestSHA := ManifestSHA256(manifestBytes)
	if report.ManifestSHA256 != manifestSHA {
		return VerifiedArtifacts{}, fmt.Errorf("verify: report manifest_sha256 %s does not match manifest bytes %s",
			report.ManifestSHA256, manifestSHA)
	}
	if report.RunID != manifest.RunID {
		return VerifiedArtifacts{}, fmt.Errorf("verify: report run_id %q does not match manifest run_id %q",
			report.RunID, manifest.RunID)
	}
	if report.PlanSHA256 != manifest.Plan.SHA256 {
		return VerifiedArtifacts{}, fmt.Errorf("verify: report plan_sha256 %s does not match manifest plan %s",
			report.PlanSHA256, manifest.Plan.SHA256)
	}
	result := VerifiedArtifacts{
		RunID:          manifest.RunID,
		State:          report.State,
		ManifestSHA256: manifestSHA,
		ReportSHA256:   ReportSHA256(reportBytes),
	}
	if err := verifyRegistryArtifact(dir, manifest, &result); err != nil {
		return VerifiedArtifacts{}, err
	}
	return result, nil
}

func verifyRegistryArtifact(dir string, manifest *Manifest, result *VerifiedArtifacts) error {
	switch manifest.Registry.Status {
	case "unavailable":
		if manifest.Registry.Artifact != nil || manifest.Registry.ArtifactSHA256 != nil {
			return errors.New("verify: unavailable registry carries an artifact")
		}
		return nil
	case "ok":
		if manifest.Registry.Artifact == nil || manifest.Registry.ArtifactSHA256 == nil {
			return errors.New("verify: available registry is missing its artifact binding")
		}
		artifactBytes, err := os.ReadFile(filepath.Join(dir, *manifest.Registry.Artifact))
		if err != nil {
			return fmt.Errorf("verify: read registry artifact: %w", err)
		}
		digest := ManifestSHA256(artifactBytes)
		if digest != *manifest.Registry.ArtifactSHA256 {
			return fmt.Errorf("verify: registry artifact digest %s does not match manifest %s",
				digest, *manifest.Registry.ArtifactSHA256)
		}
		result.RegistrySHA256 = digest
		return nil
	default:
		return fmt.Errorf("verify: registry status %q is not ok or unavailable", manifest.Registry.Status)
	}
}
