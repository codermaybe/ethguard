package ethguard

import (
	"errors"
	"testing"
)

func TestAnchorMismatchClassificationRemainsMalformedCompatible(t *testing.T) {
	err := errors.Join(ErrMalformedResponse, ErrAnchorMismatch)
	if !errors.Is(err, ErrMalformedResponse) || !errors.Is(err, ErrAnchorMismatch) {
		t.Fatal("joined anchor mismatch lost one of its classifications")
	}
}
