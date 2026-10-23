package livecanary

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeRegistryTransport struct {
	count   int
	request *http.Request
	respond func(*http.Request) (*http.Response, error)
}

func (f *fakeRegistryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.count++
	f.request = request
	return f.respond(request)
}

func fakeResponse(status int, body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(body),
	}
}

func fetchClock() time.Time {
	return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
}

func registryBody(members ...string) []byte {
	quoted := make([]string, len(members))
	for i, member := range members {
		quoted[i] = `"` + member + `"`
	}
	return []byte(`[{"chainId":1,"rpc":[` + strings.Join(quoted, ",") + `]}]`)
}

func TestFetchReturnsOneBodyAndProvenance(t *testing.T) {
	body := registryBody("https://a.example", "https://b.example", "https://c.example")
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, bytes.NewReader(body)), nil
	}}
	fetched, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(fetched.Raw, body) {
		t.Error("fetched bytes changed")
	}
	if fetched.FetchedAt != "2026-10-23T12:00:00Z" {
		t.Errorf("fetched_at = %s", fetched.FetchedAt)
	}
	if transport.count != 1 {
		t.Errorf("requests = %d, want 1", transport.count)
	}
	if transport.request.Method != http.MethodGet {
		t.Errorf("method = %s", transport.request.Method)
	}
	if transport.request.URL.String() != ChainlistURL {
		t.Errorf("url = %s", transport.request.URL)
	}
	if transport.request.Header.Get("User-Agent") != ToolVersion {
		t.Errorf("user-agent = %q", transport.request.Header.Get("User-Agent"))
	}
	if transport.request.Header.Get("Accept-Encoding") != "identity" {
		t.Errorf("accept-encoding = %q", transport.request.Header.Get("Accept-Encoding"))
	}
}

func TestFetchRejectsNon200AndRedirectWithoutRetry(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		headers http.Header
	}{
		{"server error", http.StatusInternalServerError, nil},
		{"not found", http.StatusNotFound, nil},
		{"redirect", http.StatusFound, http.Header{"Location": []string{"https://attacker.example/rpcs.json"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
				response := fakeResponse(test.status, strings.NewReader("{}"))
				for key, values := range test.headers {
					response.Header[key] = values
				}
				return response, nil
			}}
			fetched, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Fetch(context.Background())
			if err == nil {
				t.Fatal("Fetch accepted a non-200 response")
			}
			if fetched.FetchedAt != "2026-10-23T12:00:00Z" {
				t.Errorf("fetched_at = %s", fetched.FetchedAt)
			}
			if transport.count != 1 {
				t.Errorf("requests = %d, want 1", transport.count)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestFetchAppliesRegistryTimeout(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-time.After(2 * time.Second):
			return nil, errors.New("request deadline was not applied")
		}
	})
	fetched, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: 20 * time.Millisecond}).Fetch(context.Background())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fetch error = %v, want context deadline", err)
	}
	if fetched.FetchedAt == "" {
		t.Error("timeout did not record fetched_at")
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestFetchEnforcesByteLimit(t *testing.T) {
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		body := io.LimitReader(zeroReader{}, int64(RegistryMaxBytes)+1)
		return fakeResponse(http.StatusOK, body), nil
	}}
	_, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: 5 * time.Second}).Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Fetch error = %v, want byte-limit failure", err)
	}
	if transport.count != 1 {
		t.Errorf("requests = %d, want 1", transport.count)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func TestFetchHandlesBodyReadFailure(t *testing.T) {
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, failingReader{}), nil
	}}
	_, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read body") {
		t.Fatalf("Fetch error = %v", err)
	}
}

func TestFetchRejectsZeroClockBeforeRequest(t *testing.T) {
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, strings.NewReader("[]")), nil
	}}
	_, err := (Fetcher{Transport: transport, Now: func() time.Time { return time.Time{} }, Timeout: time.Second}).Fetch(context.Background())
	if err == nil {
		t.Fatal("Fetch accepted a zero fetched_at")
	}
	if transport.count != 0 {
		t.Errorf("requests = %d, want 0 before provenance is valid", transport.count)
	}
}

func TestAcquireFixedMembership(t *testing.T) {
	body := registryBody("https://a.example", "https://b.example", "https://c.example")
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, bytes.NewReader(body)), nil
	}}
	result, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), testProvenance())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if result.Membership != MembershipFixed || result.Inconclusive() {
		t.Errorf("membership = %q, inconclusive = %v", result.Membership, result.Inconclusive())
	}
	if result.Manifest.Registry.Status != "ok" {
		t.Errorf("registry status = %q", result.Manifest.Registry.Status)
	}
	if result.Manifest.Selection == nil || len(result.Manifest.Selection.Selected) != 3 {
		t.Fatalf("selected = %+v", result.Manifest.Selection)
	}
	if result.Artifact == nil {
		t.Error("fixed membership must produce an artifact")
	}
	if transport.count != 1 {
		t.Errorf("requests = %d, want 1", transport.count)
	}
}

func TestAcquireInsufficientMembers(t *testing.T) {
	body := registryBody("https://a.example", "https://b.example")
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, bytes.NewReader(body)), nil
	}}
	result, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), testProvenance())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if result.Membership != MembershipInsufficient || !result.Inconclusive() {
		t.Errorf("membership = %q, inconclusive = %v", result.Membership, result.Inconclusive())
	}
	if result.Manifest.Selection == nil || len(result.Manifest.Selection.Selected) != 2 {
		t.Fatalf("selected = %+v", result.Manifest.Selection)
	}
	if result.Artifact == nil {
		t.Error("insufficient membership must still publish the registry artifact")
	}
}

func TestAcquirePrePlanInconclusive(t *testing.T) {
	tests := []struct {
		name           string
		body           []byte
		status         int
		wantStatus     string
		wantArtifact   bool
		wantMembership MembershipState
	}{
		{name: "fetch failure", status: http.StatusInternalServerError, wantStatus: "unavailable", wantMembership: MembershipRegistryUnavailable},
		{name: "parse failure", body: []byte(`{}`), status: http.StatusOK, wantStatus: "unavailable", wantMembership: MembershipRegistryUnavailable},
		{name: "target chain missing", body: []byte(`[{"chainId":2,"rpc":["https://other.example"]}]`), status: http.StatusOK, wantStatus: "ok", wantArtifact: true, wantMembership: MembershipRegistryUnavailable},
		{name: "target chain empty", body: []byte(`[{"chainId":1,"rpc":[]}]`), status: http.StatusOK, wantStatus: "ok", wantArtifact: true, wantMembership: MembershipInsufficient},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
				return fakeResponse(test.status, bytes.NewReader(test.body)), nil
			}}
			result, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), testProvenance())
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if result.Membership != test.wantMembership || !result.Inconclusive() {
				t.Errorf("membership = %q, inconclusive = %v; want %q", result.Membership, result.Inconclusive(), test.wantMembership)
			}
			if result.Manifest.Registry.Status != test.wantStatus {
				t.Errorf("registry status = %q, want %q", result.Manifest.Registry.Status, test.wantStatus)
			}
			if (result.Artifact != nil) != test.wantArtifact {
				t.Errorf("artifact present = %v, want %v", result.Artifact != nil, test.wantArtifact)
			}
		})
	}
}

func TestAcquirePublishesFilteredReasonsAndRedacts(t *testing.T) {
	body := readTestdata(t, "registry.json")
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, bytes.NewReader(body)), nil
	}}
	result, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), testProvenance())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if result.Membership != MembershipFixed {
		t.Fatalf("membership = %q", result.Membership)
	}
	if result.Manifest.Selection.CandidateCount != 13 || len(result.Manifest.Selection.Filtered) != 8 {
		t.Errorf("candidate/filtered = %d/%d, want 13/8",
			result.Manifest.Selection.CandidateCount, len(result.Manifest.Selection.Filtered))
	}
	if result.Manifest.Registry.Redaction != RedactionURLCredentials {
		t.Errorf("redaction = %q", result.Manifest.Registry.Redaction)
	}
	if bytes.Contains(result.Artifact, []byte("SECRET")) || bytes.Contains(result.Artifact, []byte("user:secret")) {
		t.Error("artifact leaked credentials")
	}
	reasons := make(map[string]bool)
	for _, entry := range result.Manifest.Selection.Filtered {
		reasons[entry.Reason] = true
	}
	for _, reason := range []string{filterInvalidURL, filterNonHTTPS, filterCredentials, filterDuplicate} {
		if !reasons[reason] {
			t.Errorf("filtered reasons missing %q", reason)
		}
	}
}

func TestAcquireRejectsZeroGeneratedAtBeforeFetch(t *testing.T) {
	transport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, strings.NewReader("[]")), nil
	}}
	provenance := testProvenance()
	provenance.GeneratedAt = time.Time{}
	if _, err := (Fetcher{Transport: transport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), provenance); err == nil {
		t.Fatal("Acquire accepted a zero generated_at")
	}
	if transport.count != 0 {
		t.Errorf("requests = %d, want 0", transport.count)
	}
}

func TestAcquireDoesNotMergeRuns(t *testing.T) {
	firstTransport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, bytes.NewReader(registryBody("https://a.example", "https://b.example", "https://c.example"))), nil
	}}
	secondTransport := &fakeRegistryTransport{respond: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, bytes.NewReader(registryBody("https://x.example", "https://y.example", "https://z.example"))), nil
	}}
	first, err := (Fetcher{Transport: firstTransport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), testProvenance())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, err := (Fetcher{Transport: secondTransport, Now: fetchClock, Timeout: time.Second}).Acquire(context.Background(), testProvenance())
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if first.Manifest.Selection.VotingBasisSHA256 == second.Manifest.Selection.VotingBasisSHA256 {
		t.Error("different runs share a voting basis")
	}
	if len(first.Manifest.Selection.Selected) != 3 || len(second.Manifest.Selection.Selected) != 3 {
		t.Error("runs merged members")
	}
	firstManifest, err := first.Manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondManifest, err := second.Manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if bytes.Equal(firstManifest, secondManifest) {
		t.Error("different registries produced the same manifest")
	}
}

func TestMembershipStateInconclusive(t *testing.T) {
	if MembershipFixed.Inconclusive() {
		t.Error("fixed membership marked inconclusive")
	}
	for _, state := range []MembershipState{MembershipInsufficient, MembershipRegistryUnavailable} {
		if !state.Inconclusive() {
			t.Errorf("%s not marked inconclusive", state)
		}
	}
}
