package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/internal/ethobject/feehistory"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodFeeHistory is the JSON-RPC method spelling this file owns: the fee
// history range read.
const MethodFeeHistory = "eth_feeHistory"

// feeHistorySpec is the method's compiled contract: canonical fee-history
// comparison over the strict resolver, a moving reference riding the newest
// block, pending observed, and the request/response validators. No whole-key
// compatibility correction applies to this method.
func feeHistorySpec() MethodSpec {
	return MethodSpec{
		Method:      MethodFeeHistory,
		Resolver:    ResolverStrict,
		Normalize:   normalizeFeeHistory,
		Encode:      feehistory.Encode,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareMethodSpecific,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{1}},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
		Prepare: func(_ context.Context, req rpc.Request) (rpc.Request, error) {
			return req, validateFeeHistoryRequest(req)
		},
		Admit:                  admitFeeHistory,
		ObservationProjectorID: ProjectorFeeHistory,
	}
}

func feeHistoryDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodFeeHistory,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Alignment: "v1.17.10",
	}
}

func admitFeeHistory(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateFeeHistoryRequest(req); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	if err := feehistory.ValidateForRequest(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	if err := validateFeeHistoryRewardWidth(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
}

// validateFeeHistoryRewardWidth binds the response's reward rows to the
// request's percentile list. The official result derives every sample from
// the requested block percentiles, and geth emits rows of exactly that
// width when percentiles are requested and omits the reward member entirely
// when none are (omitempty on a nil slice). The request-blind normalize
// pass can only enforce row-width uniformity; a fleet that is uniformly
// wrong in the same way must not cluster into Corroborated.
func validateFeeHistoryRewardWidth(req rpc.Request, value json.RawMessage) error {
	percentiles, err := requestPercentiles(req.Params[2])
	if err != nil {
		return fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	raw, present := fields["reward"]
	if !present {
		if len(percentiles) > 0 {
			return fmt.Errorf("%w: reward absent but %d percentiles requested", rpc.ErrMalformedResponse, len(percentiles))
		}
		return nil
	}
	var rows *[]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("%w: reward: %v", rpc.ErrMalformedResponse, err)
	}
	if rows == nil {
		if len(percentiles) > 0 {
			return fmt.Errorf("%w: reward null but %d percentiles requested", rpc.ErrMalformedResponse, len(percentiles))
		}
		return nil
	}
	for i, row := range *rows {
		var samples []json.RawMessage
		if err := json.Unmarshal(row, &samples); err != nil {
			return fmt.Errorf("%w: reward row %d: %v", rpc.ErrMalformedResponse, i, err)
		}
		if len(samples) != len(percentiles) {
			return fmt.Errorf("%w: reward row %d width %d does not match the %d requested percentiles", rpc.ErrMalformedResponse, i, len(samples), len(percentiles))
		}
	}
	return nil
}

func validateFeeHistoryRequest(req rpc.Request) error {
	if len(req.Params) != 3 {
		return fmt.Errorf("%w: eth_feeHistory expects blockCount, newestBlock, rewardPercentiles", rpc.ErrInvalidRequest)
	}
	if count, err := requestQuantity(req.Params[0], false); err != nil || count == 0 {
		return fmt.Errorf("%w: eth_feeHistory blockCount must be positive", rpc.ErrInvalidRequest)
	}
	if _, err := requestQuantity(req.Params[1], true); err != nil {
		return fmt.Errorf("%w: eth_feeHistory newestBlock is invalid", rpc.ErrInvalidRequest)
	}
	percentiles, err := requestPercentiles(req.Params[2])
	if err != nil {
		return fmt.Errorf("%w: eth_feeHistory rewardPercentiles must be an array", rpc.ErrInvalidRequest)
	}
	last := -1.0
	for _, percentile := range percentiles {
		if math.IsNaN(percentile) || math.IsInf(percentile, 0) || percentile < 0 || percentile > 100 || percentile < last {
			return fmt.Errorf("%w: eth_feeHistory reward percentiles must be sorted in [0,100]", rpc.ErrInvalidRequest)
		}
		last = percentile
	}
	return nil
}

// requestQuantity accepts both JSON-shaped strings and the typed values used
// by the root FeeHistory method. Keeping this conversion in the policy means
// typed and raw callers receive identical validation before fan-out.
func requestQuantity(value any, allowTag bool) (uint64, error) {
	switch v := value.(type) {
	case string:
		if allowTag && (strings.EqualFold(v, "latest") || strings.EqualFold(v, "safe") || strings.EqualFold(v, "finalized") || strings.EqualFold(v, "pending")) {
			return 1, nil
		}
		if len(v) < 3 || !strings.HasPrefix(v, "0x") {
			return 0, fmt.Errorf("invalid quantity")
		}
		n, ok := new(big.Int).SetString(v[2:], 16)
		if !ok || n.Sign() < 0 || !n.IsUint64() {
			return 0, fmt.Errorf("invalid quantity")
		}
		return n.Uint64(), nil
	case uint64:
		return v, nil
	case uint:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case uint16:
		return uint64(v), nil
	case uint8:
		return uint64(v), nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("negative quantity")
		}
		return uint64(v), nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("negative quantity")
		}
		return uint64(v), nil
	case *big.Int:
		if v == nil || v.Sign() < 0 || !v.IsUint64() {
			return 0, fmt.Errorf("invalid quantity")
		}
		return v.Uint64(), nil
	case json.Number:
		n, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid quantity")
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported quantity type %T", value)
	}
}

func requestPercentiles(value any) ([]float64, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case []float64:
		return v, nil
	case []any:
		out := make([]float64, len(v))
		for i, item := range v {
			switch n := item.(type) {
			case float64:
				out[i] = n
			case float32:
				out[i] = float64(n)
			case json.Number:
				parsed, err := strconv.ParseFloat(string(n), 64)
				if err != nil {
					return nil, err
				}
				out[i] = parsed
			default:
				return nil, fmt.Errorf("unsupported percentile type %T", item)
			}
		}
		return out, nil
	default:
		// Permit named []float64 aliases without permitting arbitrary slices.
		rv := reflect.ValueOf(value)
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Float64 {
			out := make([]float64, rv.Len())
			for i := range out {
				out[i] = rv.Index(i).Float()
			}
			return out, nil
		}
		return nil, fmt.Errorf("unsupported percentile type %T", value)
	}
}

func normalizeFeeHistory(value json.RawMessage) (string, error) {
	return feehistory.Normalize(value)
}
