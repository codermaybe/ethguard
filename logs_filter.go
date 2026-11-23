package ethguard

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

// logsFilter is an owned, immutable interpretation of the official filter.
// Range/hash restrictions select only the Head stream's post-birth path. They
// never request history or end the stream, which must continue to retract.
type logsFilter struct {
	from, to  *uint64
	hash      *common.Hash
	addresses []string
	topics    [][]string
}

func logsFilterBytes(f logsFilter) uint64 {
	n := uint64(cap(f.addresses))*16 + uint64(cap(f.topics))*24
	for _, address := range f.addresses {
		n += uint64(len(address))
	}
	for _, alternatives := range f.topics {
		n += uint64(cap(alternatives)) * 16
		for _, topic := range alternatives {
			n += uint64(len(topic))
		}
	}
	return n
}

func compileLogsFilter(q ethapi.FilterQuery) (logsFilter, error) {
	arg, err := toFilterArg(q)
	if err != nil {
		return logsFilter{}, err
	}
	var out logsFilter
	for key, value := range arg {
		switch key {
		case "blockHash":
			h := common.HexToHash(value.(string))
			out.hash = &h
		case "fromBlock", "toBlock":
			text := value.(string)
			if text == "latest" {
				continue
			}
			if text == "safe" || text == "finalized" {
				return logsFilter{}, fmt.Errorf("%w: logs stream does not support %s", rpc.ErrInvalidRequest, text)
			}
			n, err := strconv.ParseUint(strings.TrimPrefix(text, "0x"), 16, 64)
			if err != nil {
				return logsFilter{}, fmt.Errorf("%w: logs %s is outside uint64", rpc.ErrInvalidRequest, key)
			}
			if key == "fromBlock" {
				out.from = &n
			} else {
				out.to = &n
			}
		case "address":
			out.addresses = logsAlternatives(value)
		case "topics":
			if entries, ok := value.([]any); ok {
				out.topics = make([][]string, len(entries))
				for i, entry := range entries {
					out.topics[i] = logsAlternatives(entry)
				}
			}
		default:
			return logsFilter{}, fmt.Errorf("%w: unsupported logs filter field %q", rpc.ErrInvalidRequest, key)
		}
	}
	if out.from != nil && out.to != nil && *out.from > *out.to {
		return logsFilter{}, fmt.Errorf("%w: logs fromBlock exceeds toBlock", rpc.ErrInvalidRequest)
	}
	return out, nil
}

func logsAlternatives(value any) []string {
	switch value := value.(type) {
	case string:
		return []string{value}
	case []any:
		out := make([]string, len(value))
		for i, item := range value {
			out[i] = item.(string)
		}
		return out
	default:
		return nil
	}
}

func (f logsFilter) includes(node AnchorNode) bool {
	return (f.from == nil || node.Number >= *f.from) && (f.to == nil || node.Number <= *f.to) && (f.hash == nil || *f.hash == common.Hash(node.Hash))
}

func (f logsFilter) matches(entry ethapi.Log) bool {
	if len(f.addresses) != 0 && !slices.Contains(f.addresses, string(*entry.Address)) {
		return false
	}
	if len(f.topics) > len(*entry.Topics) {
		return false
	}
	for i, alternatives := range f.topics {
		if len(alternatives) != 0 && !slices.Contains(alternatives, string((*entry.Topics)[i])) {
			return false
		}
	}
	return true
}

// A negative bloom is sufficient to prove no matching address/topic. A
// positive bloom is only a hint and must be followed by complete content proof.
func (f logsFilter) mayMatch(bloom types.Bloom) bool {
	if len(f.addresses) != 0 {
		found := false
		for _, address := range f.addresses {
			found = found || types.BloomLookup(bloom, common.HexToAddress(address))
		}
		if !found {
			return false
		}
	}
	for _, alternatives := range f.topics {
		found := len(alternatives) == 0
		for _, topic := range alternatives {
			found = found || types.BloomLookup(bloom, common.HexToHash(topic))
		}
		if !found {
			return false
		}
	}
	return true
}
