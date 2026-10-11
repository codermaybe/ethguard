package block

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
)

// Encode returns the canonical block value for the fields whose commitments
// are checked by AdmitBlock. Header JSON comes from go-ethereum's typed
// header encoder; transaction and withdrawal bodies are re-encoded through
// their typed forms. Response metadata such as size, totalDifficulty, from,
// and provider extensions is deliberately left outside the protected value.
//
// A header-only response remains valid: body fields are included only when
// present in the source payload. Full-body fields are canonicalized when they
// are present, while withdrawals are included only after the header commits
// to them.
func Encode(raw json.RawMessage) (json.RawMessage, error) {
	return encode(raw, false)
}

// EncodeByNumber preserves the source spelling for a header-only
// eth_getBlockByNumber response. The number policy deliberately keeps that
// narrow, source-shaped Value while its comparison key folds number and hash;
// complete typed block responses still use canonical header encoding.
func EncodeByNumber(raw json.RawMessage) (json.RawMessage, error) {
	return encode(raw, true)
}

func encode(raw json.RawMessage, preserveHeadSpelling bool) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage("null"), nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &source); err != nil {
		return nil, fmt.Errorf("block encode object: %w", err)
	}
	var header *types.Header
	if err := json.Unmarshal(trimmed, &header); err != nil || header == nil {
		if hasBlockHead(source) {
			return encodeHeaderOnly(source, preserveHeadSpelling)
		}
		// Let the block normalizer report a missing identity for malformed or
		// incomplete responses. Header-only anchor reads intentionally need only
		// number and hash; full-body calls are rejected by AdmitBlock first.
		return json.Marshal(source)
	}
	headerRaw, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("block encode canonical header: %w", err)
	}
	value := make(map[string]json.RawMessage)
	if err := json.Unmarshal(headerRaw, &value); err != nil {
		return nil, fmt.Errorf("block encode canonical header object: %w", err)
	}
	if body, ok := source["transactions"]; ok {
		encoded, err := encodeTransactions(body)
		if err != nil {
			return nil, err
		}
		value["transactions"] = encoded
	}
	if body, ok := source["uncles"]; ok {
		encoded, err := encodeHashes(body, "uncles")
		if err != nil {
			return nil, err
		}
		value["uncles"] = encoded
	}
	if header.WithdrawalsHash != nil {
		body, ok := source["withdrawals"]
		if ok {
			if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
				return nil, fmt.Errorf("block encode withdrawals: committed body is null")
			}
			encoded, err := encodeWithdrawals(body)
			if err != nil {
				return nil, err
			}
			value["withdrawals"] = encoded
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("block encode value: %w", err)
	}
	return encoded, nil
}

func hasBlockHead(source map[string]json.RawMessage) bool {
	_, number := source["number"]
	_, hash := source["hash"]
	return number && hash
}

func encodeHeaderOnly(source map[string]json.RawMessage, preserveSpelling bool) (json.RawMessage, error) {
	var number, hash string
	if err := json.Unmarshal(source["number"], &number); err != nil {
		return json.Marshal(source)
	}
	if err := json.Unmarshal(source["hash"], &hash); err != nil {
		return json.Marshal(source)
	}
	height, err := rpc.ParseHexQuantity(number)
	if err != nil {
		return json.Marshal(source)
	}
	if preserveSpelling {
		return json.Marshal(map[string]string{"hash": hash, "number": number})
	}
	return json.Marshal(map[string]string{
		"hash":   strings.ToLower(hash),
		"number": fmt.Sprintf("0x%x", height),
	})
}

func encodeTransactions(raw json.RawMessage) (json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		if err == nil {
			err = fmt.Errorf("body is null")
		}
		return nil, fmt.Errorf("block encode transactions: %w", err)
	}
	out := make([]json.RawMessage, len(items))
	for i, item := range items {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) > 0 && trimmed[0] == '"' {
			var hash common.Hash
			if err := json.Unmarshal(trimmed, &hash); err != nil {
				return nil, fmt.Errorf("block encode transaction hash %d: %w", i, err)
			}
			out[i], _ = json.Marshal(hash)
			continue
		}
		var tx types.Transaction
		if err := json.Unmarshal(trimmed, &tx); err != nil {
			return nil, fmt.Errorf("block encode transaction %d: %w", i, err)
		}
		encoded, err := json.Marshal(&tx)
		if err != nil {
			return nil, fmt.Errorf("block encode transaction %d: %w", i, err)
		}
		out[i] = encoded
	}
	return json.Marshal(out)
}

func encodeHashes(raw json.RawMessage, name string) (json.RawMessage, error) {
	var hashes []common.Hash
	if err := json.Unmarshal(raw, &hashes); err != nil || hashes == nil {
		if err == nil {
			err = fmt.Errorf("body is null")
		}
		return nil, fmt.Errorf("block encode %s: %w", name, err)
	}
	return json.Marshal(hashes)
}

func encodeWithdrawals(raw json.RawMessage) (json.RawMessage, error) {
	var withdrawals []*types.Withdrawal
	if err := json.Unmarshal(raw, &withdrawals); err != nil {
		return nil, fmt.Errorf("block encode withdrawals: %w", err)
	}
	for i, withdrawal := range withdrawals {
		if withdrawal == nil {
			return nil, fmt.Errorf("block encode withdrawal %d: null", i)
		}
	}
	return json.Marshal(withdrawals)
}
