package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	logobject "github.com/codermaybe/ethguard/internal/ethobject/log"
	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodGetLogs is the JSON-RPC method spelling this file owns: the filter
// read, and the read plan the logs subscription Pull reuses.
const MethodGetLogs = "eth_getLogs"

// getLogsSpec is the method's compiled contract: canonical log-list
// comparison over the strict resolver, a range reference over the filter's
// height fields, pending refused locally, and the list admission binding.
// No whole-key compatibility correction applies to this method.
func getLogsSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetLogs,
		Resolver:               ResolverStrict,
		Normalize:              normalizeLogs,
		Encode:                 logobject.Encode,
		KeySource:              rpc.RuleKeyCanonicalBytes,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Reference:              ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}},
		Pending:                PendingSpec{Mode: PendingRefuse, Pos: 0},
		Prepare:                func(_ context.Context, req rpc.Request) (rpc.Request, error) { return req, validateLogRequest(req) },
		Admit:                  admitLogs,
		ObservationProjectorID: ProjectorLogs,
	}
}

func getLogsDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetLogs,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "FilterLogs",
		Alignment: "v1.17.10",
	}
}

func admitLogs(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateLogsResponse(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
}

// This file is the Log object slice: Log validation (requiredness) and the
// logs projection.
//
// Ownership split per R2: the generated mask (shared with the receipt slice)
// owns the Log WHITELIST — which fields participate in comparison; this file
// owns Log REQUIREDNESS — which fields must be present for a log to vote.
// Requiredness stays hand-written because the spec union spans variants a
// single response never carries; the whitelist stays generated because it
// must track the standard field-for-field.

func validateLogRequest(req rpc.Request) error {
	if len(req.Params) != 1 {
		return fmt.Errorf("%w: eth_getLogs expects one filter", rpc.ErrInvalidRequest)
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		// Keep the policy usable by the generic anchor tests and callers that
		// perform their own typed decoding. The adapter supplies an object and
		// receives the full filter validation below.
		return nil
	}
	var (
		fromBlock any
		toBlock   any
		hasFrom   bool
		hasTo     bool
	)
	for _, key := range []string{"fromBlock", "toBlock"} {
		value, present := filter[key]
		if !present {
			continue
		}
		if key == "fromBlock" {
			fromBlock, hasFrom = value, true
		} else {
			toBlock, hasTo = value, true
		}
		if pendingTag(value) {
			return fmt.Errorf("%w: eth_getLogs %s=pending has no stable corroboration semantics", rpc.ErrInvalidRequest, key)
		}
		if err := validateLogBound(key, value); err != nil {
			return err
		}
	}
	// Numeric ranges have an unambiguous ordering and an inverted range is a
	// request error. Moving tags (latest/safe/finalized) deliberately stay
	// incomparable here; the reference preparation phase owns their pinning.
	if hasFrom && hasTo {
		from, fromNumeric := logBoundQuantity(fromBlock)
		to, toNumeric := logBoundQuantity(toBlock)
		if fromNumeric && toNumeric && from > to {
			return fmt.Errorf("%w: eth_getLogs fromBlock is greater than toBlock", rpc.ErrInvalidRequest)
		}
	}
	if value, present := filter["blockHash"]; present && pendingTag(value) {
		return fmt.Errorf("%w: eth_getLogs blockHash=pending has no stable corroboration semantics", rpc.ErrInvalidRequest)
	}
	if _, hasHash := filter["blockHash"]; hasHash {
		if _, hasFrom := filter["fromBlock"]; hasFrom {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with fromBlock", rpc.ErrInvalidRequest)
		}
		if _, hasTo := filter["toBlock"]; hasTo {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with toBlock", rpc.ErrInvalidRequest)
		}
	}
	return nil
}

// logBoundQuantity extracts a numeric block bound without changing the
// accepted moving-tag grammar. It is intentionally best-effort: tags are
// incomparable and continue through the anchor rewrite, while malformed
// values are reported by validateLogBound.
func logBoundQuantity(value any) (uint64, bool) {
	switch typed := value.(type) {
	case string:
		if len(typed) < 2 || (typed[:2] != "0x" && typed[:2] != "0X") {
			return 0, false
		}
		quantity, err := rpc.ParseHexQuantity(typed)
		return quantity, err == nil
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return 0, false
		}
		return logBoundQuantity(decoded)
	default:
		return 0, false
	}
}

// pendingTag reports spellings that mean the pending tag, including case
// variants and non-string encodings that would otherwise skip the string
// check and reach an anchor or a provider.
// pendingTag reports spellings that mean the pending tag. It delegates to
// the rpc package's single pending-grammar authority so request validation
// and the plan layers can never drift apart on what counts as pending.
func pendingTag(value any) bool {
	return rpc.IsPendingReference(value)
}

// validateLogBound rejects empty and wrong-typed range fields locally.
// Recognized non-pending tags and quantities pass through for anchoring.
func validateLogBound(key string, value any) error {
	switch typed := value.(type) {
	case nil:
		return fmt.Errorf("%w: eth_getLogs %s is empty", rpc.ErrInvalidRequest, key)
	case string:
		if strings.TrimSpace(typed) == "" {
			return fmt.Errorf("%w: eth_getLogs %s is empty", rpc.ErrInvalidRequest, key)
		}
		return nil
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return fmt.Errorf("%w: eth_getLogs %s has an unsupported type", rpc.ErrInvalidRequest, key)
		}
		if pendingTag(decoded) {
			return fmt.Errorf("%w: eth_getLogs %s=pending has no stable corroboration semantics", rpc.ErrInvalidRequest, key)
		}
		return validateLogBound(key, decoded)
	default:
		return fmt.Errorf("%w: eth_getLogs %s has an unsupported type", rpc.ErrInvalidRequest, key)
	}
}

// normalizeLogs validates every log row, then reduces the array to its pruned
// comparison key. Ordering and duplicates stay significant; hexadecimal casing
// and vendor extension fields do not create false disagreement.
func normalizeLogs(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	var rawLogs []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawLogs); err != nil || string(trimmed) == "null" {
		return "", fmt.Errorf("logs: expected an array")
	}
	for i, raw := range rawLogs {
		if err := logobject.Validate(raw); err != nil {
			return "", fmt.Errorf("logs[%d]: %w", i, err)
		}
	}
	return wiremask.PruneMethod(MethodGetLogs, trimmed)
}

// validateLogsResponse is intentionally stricter than types.Log's JSON
// decoder.  The decoder assigns zero values to omitted fields, which would
// allow an incomplete response to become a valid vote.
func validateLogsResponse(req rpc.Request, value json.RawMessage) error {
	return logobject.ValidateResponse(req, value)
}
