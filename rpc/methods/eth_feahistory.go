package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"ethguard/rpc"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

// MethodFeeHistory is the JSON-RPC method name resolved by FeeHistoryPolicy.
const MethodFeeHistory = "eth_feeHistory"

// FeeHistoryPolicy verifies eth_feeHistory by full agreement over the whole
// response. The range is chain-determined once newest is pinned: the base
// fees and gas ratios of historical blocks are immutable, the block count
// is the caller's fixed input, and the newest+1 entry is the EIP-1559
// prediction derived from the newest block itself.
//
// The wire form constrains the anchor shape: feeHistory's newestBlock is a
// BlockNumber, not a BlockNumberOrHash, so a 32-byte hash cannot ride the
// wire — the moving tag is rewritten to the verified reference's height
// instead (see rpc.AnchorHeightFunc). Cross-client rounding of the reward
// percentiles is the known liveness gate: until a soak measures it, rounding
// differences fail closed as conflict.
func FeeHistoryPolicy(height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	inner := feeHistoryPolicy{Policy: rpc.Strict(normalizeFeeHistory, opts...)}
	return rpc.WithAnchorHeight(height, 1)(inner)
}

// feeHistoryPolicy validates the request shape and normalizes only the
// protocol fields of the response. Array relationships are checked without
// silently shrinking a requested range.
type feeHistoryPolicy struct{ rpc.Policy }

func (p feeHistoryPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	if err := validateFeeHistoryRequest(req); err != nil {
		return rpc.Request{}, err
	}
	return p.Policy.Prepare(ctx, req)
}
func (p feeHistoryPolicy) Normalize(value json.RawMessage) (string, error) {
	return normalizeFeeHistory(value)
}
func (p feeHistoryPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.Resolve(obs)
}
func (p feeHistoryPolicy) RequiresAgreement() bool { return true }
func (p feeHistoryPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if policy, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return policy.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}

// Admit applies the same request-independent response checks used by the
// normalizer before a fee-history answer can participate in voting.
func (p feeHistoryPolicy) Admit(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateFeeHistoryRequest(req); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	if _, err := normalizeFeeHistory(value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
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
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || trimmed[0] != '{' || bytes.Equal(trimmed, []byte("null")) {
		return "", fmt.Errorf("feeHistory: expected an object")
	}
	var raw struct {
		Oldest json.RawMessage     `json:"oldestBlock"`
		Base   []json.RawMessage   `json:"baseFeePerGas"`
		Gas    []float64           `json:"gasUsedRatio"`
		Reward [][]json.RawMessage `json:"reward"`
	}
	if err := json.Unmarshal(value, &raw); err != nil || len(raw.Oldest) == 0 {
		return "", fmt.Errorf("feeHistory: malformed response")
	}
	oldest, err := normalizeHexQuantity(raw.Oldest)
	if err != nil || len(raw.Base) != len(raw.Gas)+1 {
		return "", fmt.Errorf("feeHistory: baseFeePerGas must have gasUsedRatio length plus one")
	}
	base := make([]string, len(raw.Base))
	for i, item := range raw.Base {
		base[i], err = normalizeHexQuantity(item)
		if err != nil {
			return "", fmt.Errorf("feeHistory: baseFeePerGas[%d]: %w", i, err)
		}
	}
	for i, ratio := range raw.Gas {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
			return "", fmt.Errorf("feeHistory: gasUsedRatio[%d] out of range", i)
		}
	}
	if raw.Reward != nil && len(raw.Reward) != len(raw.Gas) {
		return "", fmt.Errorf("feeHistory: reward must have gasUsedRatio length")
	}
	reward := make([][]string, len(raw.Reward))
	rewardWidth := -1
	for i, row := range raw.Reward {
		if rewardWidth < 0 {
			rewardWidth = len(row)
		} else if len(row) != rewardWidth {
			return "", fmt.Errorf("feeHistory: reward rows must have equal length")
		}
		reward[i] = make([]string, len(row))
		for j, item := range row {
			reward[i][j], err = normalizeHexQuantity(item)
			if err != nil {
				return "", fmt.Errorf("feeHistory: reward[%d][%d]: %w", i, j, err)
			}
		}
	}
	projection := struct {
		Oldest string     `json:"oldestBlock"`
		Base   []string   `json:"baseFeePerGas"`
		Gas    []float64  `json:"gasUsedRatio"`
		Reward [][]string `json:"reward,omitempty"`
	}{oldest, base, raw.Gas, reward}
	encoded, err := json.Marshal(projection)
	return string(encoded), err
}
