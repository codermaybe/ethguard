package ethguard

import (
	"encoding/json"
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
	checked, err := freezeFilterQuery(q)
	if err != nil {
		return logsFilter{}, err
	}
	var out logsFilter
	var address *ethapi.FilterAddress
	var topics **[]ethapi.FilterTopic
	var from, to *ethapi.FilterBlock
	var extras map[string]json.RawMessage
	if query := checked.FilterByBlockRange; query != nil {
		address, topics, extras = query.Address, query.Topics, query.AdditionalProperties
		from, to = query.FromBlock, query.ToBlock
		start := uint64(0)
		out.from = &start
	} else {
		query := checked.FilterByBlockHash
		address, topics, extras = query.Address, query.Topics, query.AdditionalProperties
		hash := common.HexToHash(string(query.BlockHash))
		out.hash = &hash
	}
	for key := range extras {
		return logsFilter{}, fmt.Errorf("%w: unsupported logs filter field %q", rpc.ErrInvalidRequest, key)
	}
	if from != nil {
		out.from, err = logsFilterBound("fromBlock", *from)
		if err != nil {
			return logsFilter{}, err
		}
	}
	if to != nil {
		out.to, err = logsFilterBound("toBlock", *to)
		if err != nil {
			return logsFilter{}, err
		}
	}
	if out.from != nil && out.to != nil && *out.from > *out.to {
		return logsFilter{}, fmt.Errorf("%w: logs fromBlock exceeds toBlock", rpc.ErrInvalidRequest)
	}
	if address != nil {
		if address.Address != nil {
			out.addresses = []string{string(*address.Address)}
		} else if address.Addresses != nil {
			out.addresses = make([]string, len(*address.Addresses))
			for i, value := range *address.Addresses {
				out.addresses[i] = string(value)
			}
		}
	}
	if topics != nil && *topics != nil {
		out.topics = make([][]string, len(**topics))
		for i, topic := range **topics {
			if topic.SingleTopicMatch != nil {
				out.topics[i] = []string{string(*topic.SingleTopicMatch)}
			} else if topic.MultipleTopicMatch != nil {
				out.topics[i] = make([]string, len(*topic.MultipleTopicMatch))
				for j, value := range *topic.MultipleTopicMatch {
					out.topics[i][j] = string(value)
				}
			}
		}
	}
	return out, nil
}

// logsFilterBound consumes a union already validated by freezeFilterQuery.
// latest follows the live path; earliest is the explicit zero height.
func logsFilterBound(field string, bound ethapi.FilterBlock) (*uint64, error) {
	if bound.BlockTag != nil {
		switch tag := string(*bound.BlockTag); tag {
		case "latest":
			return nil, nil
		case "earliest":
			zero := uint64(0)
			return &zero, nil
		default:
			return nil, fmt.Errorf("%w: logs stream does not support %s", rpc.ErrInvalidRequest, tag)
		}
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(string(*bound.BlockNumber), "0x"), 16, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: logs %s is outside uint64", rpc.ErrInvalidRequest, field)
	}
	return &n, nil
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
