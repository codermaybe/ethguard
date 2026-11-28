package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

// FreezeFilterQuery gives unary and streaming filters an owned canonical
// model, including the method-level exclusion of hash and range selectors.
func FreezeFilterQuery(q ethapi.FilterQuery) (ethapi.FilterQuery, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return ethapi.FilterQuery{}, fmt.Errorf("%w: filter query: %v", rpc.ErrInvalidRequest, err)
	}
	raw, err = foldFilterSpelling(raw)
	if err != nil {
		return ethapi.FilterQuery{}, fmt.Errorf("%w: filter query: %v", rpc.ErrInvalidRequest, err)
	}
	checked, err := ethapi.NewFilterQuery(raw)
	if err != nil {
		return ethapi.FilterQuery{}, fmt.Errorf("%w: filter query: %v", rpc.ErrInvalidRequest, err)
	}
	if hash := checked.FilterByBlockHash; hash != nil {
		// The hash schema is open, so this exclusion belongs to the method.
		_, from := hash.AdditionalProperties["fromBlock"]
		_, to := hash.AdditionalProperties["toBlock"]
		if from || to {
			return ethapi.FilterQuery{}, fmt.Errorf("ethguard: cannot specify both block hash and from/to blocks: %w", rpc.ErrInvalidRequest)
		}
	}
	return checked, nil
}

// FilterArgument re-validates the generated filter union and spells the wire
// argument from the constructor's canonical copy: the re-validated model is
// re-marshalled and that frozen canonical spelling is what the request
// carries, so directly constructed values ride the wire canonicalized
// (lowercase addresses and hashes) rather than in their original case.
// Address, topics and open-object extras pass through the canonical bytes
// exactly, never through float64. The range branch keeps the project
// defaults — a missing fromBlock stays "0x0" and a missing toBlock "latest",
// rewrites the earliest tag to the
// explicit 0x0 height, and rejects pending bounds locally. The hash branch
// is an open object, so from/to members cannot be schema-forbidden there;
// the method's own mutual exclusion rejects them as an input error.
func FilterArgument(q ethapi.FilterQuery) (map[string]any, error) {
	checked, err := FreezeFilterQuery(q)
	if err != nil {
		return nil, err
	}
	// Consume the constructor's canonical copy: the original bytes are
	// discarded after validation, so the wire spelling is the audited
	// canonical one.
	canonical, err := json.Marshal(checked)
	if err != nil {
		return nil, fmt.Errorf("%w: filter query: %v", rpc.ErrInvalidRequest, err)
	}
	var members map[string]any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&members); err != nil {
		return nil, fmt.Errorf("%w: filter query: %v", rpc.ErrInvalidRequest, err)
	}
	arg := make(map[string]any, len(members))
	if rawHash, present := members["blockHash"]; present {
		hashText, _ := rawHash.(string)
		arg["blockHash"] = hashText
	} else {
		fromBlock := "0x0"
		if raw, present := members["fromBlock"]; present {
			if fromBlock, err = filterBound(raw); err != nil {
				return nil, err
			}
		}
		toBlock := "latest"
		if raw, present := members["toBlock"]; present {
			if toBlock, err = filterBound(raw); err != nil {
				return nil, err
			}
		}
		arg["fromBlock"] = fromBlock
		arg["toBlock"] = toBlock
	}
	for _, field := range []string{"address", "topics"} {
		if raw, present := members[field]; present {
			arg[field] = raw
		}
	}
	// Open-object extras ride the frozen canonical bytes exactly as
	// declared — the generated parser has already checked their JSON safety.
	for key, value := range members {
		switch key {
		case "address", "topics", "fromBlock", "toBlock", "blockHash":
			continue
		}
		arg[key] = value
	}
	return arg, nil
}

// filterBound spells one declared range bound. Canonical quantities pass
// through; the earliest tag keeps the explicit 0x0 height contract; pending
// is rejected locally before any reference establishment.
func filterBound(raw any) (string, error) {
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: filter block bound must be a string", rpc.ErrInvalidRequest)
	}
	if strings.EqualFold(text, "pending") {
		return "", fmt.Errorf("ethguard: pending filter bound is not supported: %w", rpc.ErrInvalidRequest)
	}
	if strings.EqualFold(text, "earliest") {
		return "0x0", nil
	}
	return text, nil
}
