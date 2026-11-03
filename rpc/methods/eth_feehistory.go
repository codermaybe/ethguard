package methods

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/feehistory"
)

func validateFeeHistoryRange(req rpc.Request, value json.RawMessage) error {
	return feehistory.ValidateRange(req, value)
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
