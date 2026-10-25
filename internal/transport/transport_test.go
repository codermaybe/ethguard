package transport

import (
	"errors"
	"testing"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestAdaptErrorPreservesHTTPStatusCapability(t *testing.T) {
	got := adaptError(gethrpc.HTTPError{StatusCode: 429, Status: "429 Too Many Requests"})
	var status interface{ HTTPStatusCode() int }
	if !errors.As(got, &status) || status.HTTPStatusCode() != 429 {
		t.Fatalf("adapted error status = %v, want 429", got)
	}
	var rateLimit interface{ IsRateLimit() bool }
	if !errors.As(got, &rateLimit) || !rateLimit.IsRateLimit() {
		t.Fatalf("adapted error lost rate-limit capability: %v", got)
	}
	var original gethrpc.HTTPError
	if !errors.As(got, &original) || original.StatusCode != 429 {
		t.Fatalf("adapted error lost original HTTPError: %v", got)
	}
}
