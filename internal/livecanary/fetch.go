package livecanary

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FetchedRegistry is one successful registry download with its fetch time.
type FetchedRegistry struct {
	Raw       []byte
	FetchedAt string
}

// Fetcher performs the single registry acquisition for one live run. It has no
// retry loop and refuses redirects so registry.url keeps pointing at the host
// that actually served the bytes.
type Fetcher struct {
	Transport http.RoundTripper
	Now       func() time.Time
	Timeout   time.Duration
}

func (f Fetcher) Fetch(ctx context.Context) (FetchedRegistry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := f.Now
	if now == nil {
		now = time.Now
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = time.Duration(RegistryTimeoutMS) * time.Millisecond
	}
	transport := f.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if _, err := formatProvenanceTime("registry.fetched_at", now()); err != nil {
		return FetchedRegistry{}, err
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ChainlistURL, nil)
	if err != nil {
		return FetchedRegistry{}, fmt.Errorf("registry: build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", ToolVersion)

	response, err := client.Do(request)
	fetchedAt, timeErr := formatProvenanceTime("registry.fetched_at", now())
	if timeErr != nil {
		if response != nil {
			response.Body.Close()
		}
		return FetchedRegistry{}, timeErr
	}
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		return FetchedRegistry{FetchedAt: fetchedAt}, fmt.Errorf("registry: fetch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return FetchedRegistry{FetchedAt: fetchedAt}, fmt.Errorf("registry: fetch: unexpected status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, RegistryMaxBytes+1))
	if err != nil {
		return FetchedRegistry{FetchedAt: fetchedAt}, fmt.Errorf("registry: read body: %w", err)
	}
	if len(body) > RegistryMaxBytes {
		return FetchedRegistry{FetchedAt: fetchedAt}, fmt.Errorf("registry: response exceeds %d bytes", RegistryMaxBytes)
	}
	return FetchedRegistry{Raw: body, FetchedAt: fetchedAt}, nil
}

// MembershipState is the pre-plan outcome of fixing the voting basis.
type MembershipState string

const (
	MembershipFixed        MembershipState = "fixed"
	MembershipInsufficient MembershipState = "insufficient_members"
	// MembershipRegistryUnavailable includes a missing target chain; in that
	// case the registry bytes themselves parsed, so manifest registry.status
	// can still be ok.
	MembershipRegistryUnavailable MembershipState = "registry_unavailable"
)

func (s MembershipState) Inconclusive() bool {
	return s != MembershipFixed
}

// AcquireResult freezes the selected basis for this run. Callers must use
// Manifest.Selection as-is and must not refetch the registry to replace it.
type AcquireResult struct {
	Manifest      Manifest
	Artifact      []byte
	RegistryError error
	Membership    MembershipState
}

func (r AcquireResult) Inconclusive() bool {
	return r.Membership.Inconclusive()
}

// Acquire performs exactly one registry fetch and one selection. Fetch and
// parse failures still produce a valid pre-plan manifest; they are reported as
// inconclusive rather than as a successful run.
func (f Fetcher) Acquire(ctx context.Context, provenance Provenance) (AcquireResult, error) {
	if _, err := formatProvenanceTime("generated_at", provenance.GeneratedAt); err != nil {
		return AcquireResult{}, err
	}
	fetched, fetchErr := f.Fetch(ctx)
	if fetchErr != nil {
		built, err := BuildUnavailable(fetched.FetchedAt, provenance)
		if err != nil {
			return AcquireResult{}, err
		}
		return AcquireResult{
			Manifest:      built.Manifest,
			RegistryError: fetchErr,
			Membership:    MembershipRegistryUnavailable,
		}, nil
	}

	built, err := BuildRegistry(fetched.FetchedAt, fetched.Raw, provenance)
	if err != nil {
		return AcquireResult{}, err
	}
	membership := MembershipFixed
	switch {
	case built.RegistryError != nil:
		membership = MembershipRegistryUnavailable
	case !built.TargetChainFound:
		membership = MembershipRegistryUnavailable
	case built.Manifest.Selection == nil || len(built.Manifest.Selection.Selected) < MinMembers:
		membership = MembershipInsufficient
	}
	return AcquireResult{
		Manifest:      built.Manifest,
		Artifact:      built.Artifact,
		RegistryError: built.RegistryError,
		Membership:    membership,
	}, nil
}
