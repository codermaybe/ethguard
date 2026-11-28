package rpcengine

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// ReferencePurpose distinguishes a business pin from a standalone query that
// promises a corroborated anchor. Both consume the same establishment round.
type ReferencePurpose uint8

const (
	ReferencePurposeBusiness ReferencePurpose = iota
	ReferencePurposeStandaloneAnchor
)

// ValidateReferenceEvidence is the shared admissibility rule for established
// references. A business pin may use Forwarded evidence only on the invocation's
// frozen singleton basis; standalone anchors always require corroboration.
// Height has already been decoded by establishment, where a missing or invalid
// number is rejected. Zero is a valid genesis height, not an absence marker.
func ValidateReferenceEvidence(purpose ReferencePurpose, basis Basis, tag string, ref rpc.ReferenceEvidence) error {
	if !EvidenceServesTag(ref, tag) {
		return fmt.Errorf("anchor: %w", rpc.ErrReferenceRequired)
	}
	switch purpose {
	case ReferencePurposeStandaloneAnchor:
		if ref.Status == rpc.Corroborated {
			return nil
		}
	case ReferencePurposeBusiness:
		switch basis.Corroboration.Mode {
		case rpc.CorroborationMajority, rpc.CorroborationUnanimity:
			if ref.Status == rpc.Corroborated {
				return nil
			}
		case rpc.CorroborationForwarded:
			if ref.Status != rpc.Forwarded || basis.Corroboration.Floor != 0 || basis.Size() != 1 {
				break
			}
			member := basis.Members[0]
			if member.ID == "" || ref.Adjudication.Basis != 1 || ref.Adjudication.Required != 1 || ref.Adjudication.Reason != rpc.ReasonAccepted ||
				len(ref.Participants) != 1 || ref.Participants[0] != member ||
				len(ref.Support) != 1 || ref.Support[0] != member || len(ref.Reject) != 0 {
				return fmt.Errorf("anchor: forwarded reference does not match the frozen source: %w", rpc.ErrGuaranteeNotMet)
			}
			if len(ref.Hash) != 66 || !strings.HasPrefix(ref.Hash, "0x") {
				return fmt.Errorf("anchor: invalid forwarded reference hash: %w", rpc.ErrMalformedResponse)
			}
			var decoded [32]byte
			if _, err := hex.Decode(decoded[:], []byte(ref.Hash[2:])); err != nil {
				return fmt.Errorf("anchor: invalid forwarded reference hash: %w", rpc.ErrMalformedResponse)
			}
			return nil
		}
	default:
		return fmt.Errorf("anchor: unknown reference purpose: %w", rpc.ErrInvalidRequest)
	}
	return fmt.Errorf("anchor: reference does not meet the required guarantee: %w", rpc.ErrGuaranteeNotMet)
}
