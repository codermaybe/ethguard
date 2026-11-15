// The remaining per-method typed projections: every decoder here owns the
// wire-to-value shape for exactly one method's resolved value. There is no
// universal decoder by design — null, pending, NotFound and malformed
// semantics stay method-specific and visible at each entry point.
package typed

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
	logobject "github.com/codermaybe/ethguard/internal/ethobject/log"
)

// jsonNull reports whether the raw value is JSON null (whitespace tolerated).
// A method answer that is null is a shape question, not a decode default:
// decoders that own a null rule must see through surrounding whitespace, or a
// padded null would slip past the check and take the zero-value path.
func jsonNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// DecodeAccessListResult returns the official generated object with optional
// field presence intact, rather than a second handwritten business structure.
func DecodeAccessListResult(raw json.RawMessage) (*ethapi.AccessListResult, error) {
	value, err := ethapi.NewAccessListResult(raw)
	if err != nil {
		return nil, wrapMalformed("decode access list result", err)
	}
	return &value, nil
}

// DecodeFeeHistory preserves the complete official generated fee-history value,
// including exact numeric ratios and optional blob-fee fields.
func DecodeFeeHistory(raw json.RawMessage) (*ethapi.FeeHistoryResult, error) {
	value, err := ethapi.NewFeeHistoryResult(raw)
	if err != nil {
		return nil, wrapMalformed("decode fee history", err)
	}
	return &value, nil
}

// DecodeGasEstimate shapes one corroborated eth_estimateGas quantity.
func DecodeGasEstimate(raw json.RawMessage) (uint64, error) {
	if jsonNull(raw) {
		return 0, wrapMalformed("decode gas estimate: JSON null", nil)
	}
	var gas hexutil.Uint64
	if err := json.Unmarshal(raw, &gas); err != nil {
		return 0, wrapMalformed("decode gas estimate", err)
	}
	return uint64(gas), nil
}

// DecodeLogs shapes one corroborated eth_getLogs array. A JSON null payload
// is not an empty result: a log query answers with an array, so null —
// including whitespace-padded spellings, which the trim check sees through —
// fails malformed instead of collapsing into an empty slice. The official
// result anyOf can match an empty array through the hash branch first; the
// shared log encoder rejects hash elements, so an empty array is the only
// legal way to answer "no logs" and every element is projected through the
// generated log constructor.
func DecodeLogs(raw json.RawMessage) ([]ethapi.Log, error) {
	if jsonNull(raw) {
		return nil, wrapMalformed("log array is JSON null", nil)
	}
	canonical, err := logobject.Encode(raw)
	if err != nil {
		return nil, wrapMalformed("decode logs", err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(canonical, &rows); err != nil {
		return nil, wrapMalformed("decode logs", err)
	}
	logs := make([]ethapi.Log, 0, len(rows))
	for i, row := range rows {
		value, err := ethapi.NewLog(row)
		if err != nil {
			return nil, wrapMalformed(fmt.Sprintf("decode logs[%d]", i), err)
		}
		logs = append(logs, value)
	}
	return logs, nil
}

// DecodeSyncProgress returns the official generated progress object. A false
// result means the source reports no ongoing sync. Provider-specific counters
// are delivered separately by the execution evidence channels.
func DecodeSyncProgress(raw json.RawMessage) (*ethapi.SyncProgress, error) {
	status, err := ethapi.NewSyncingResult(raw)
	if err != nil {
		return nil, wrapMalformed("decode sync progress", err)
	}
	if status.NotSyncing != nil && *status.NotSyncing {
		return nil, wrapMalformed("syncing boolean true is not a shape sources return", nil)
	}
	return status.SyncingProgress, nil
}
