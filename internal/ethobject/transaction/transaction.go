// Package transaction owns transaction representation, payload integrity and
// request binding shared by method execution, typed projection and subscriptions.
package transaction

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

func requestedHash(req rpc.Request) (common.Hash, error) {
	var hash common.Hash
	if len(req.Params) != 1 {
		return hash, fmt.Errorf("%w: expected one transaction hash", rpc.ErrInvalidRequest)
	}
	s, ok := req.Params[0].(string)
	if !ok || !common.IsHexHash(s) {
		return hash, fmt.Errorf("%w: transaction hash must be a 32-byte hex string", rpc.ErrInvalidRequest)
	}
	return common.HexToHash(s), nil
}

func requestBlockHash(value any) (common.Hash, error) {
	s, ok := value.(string)
	if !ok || !common.IsHexHash(s) {
		return common.Hash{}, fmt.Errorf("%w: block hash must be a 32-byte hex string", rpc.ErrInvalidRequest)
	}
	return common.HexToHash(s), nil
}

func requestIndex(value any) (uint64, error) {
	switch v := value.(type) {
	case uint:
		return uint64(v), nil
	case uint64:
		return v, nil
	case hexutil.Uint64:
		return uint64(v), nil
	case json.Number:
		index, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: transaction index: %v", rpc.ErrInvalidRequest, err)
		}
		return index, nil
	case string:
		decimal, err := ethcodec.NormalizeWireQuantity(json.RawMessage(fmt.Sprintf("%q", v)))
		if err != nil {
			return 0, fmt.Errorf("%w: transaction index: %v", rpc.ErrInvalidRequest, err)
		}
		var out uint64
		if _, err := fmt.Sscanf(decimal, "%d", &out); err != nil {
			return 0, fmt.Errorf("%w: transaction index: %v", rpc.ErrInvalidRequest, err)
		}
		return out, nil
	default:
		return 0, fmt.Errorf("%w: transaction index has unsupported type %T", rpc.ErrInvalidRequest, value)
	}
}
