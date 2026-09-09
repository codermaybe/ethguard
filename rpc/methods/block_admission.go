package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"

	"ethguard/rpc"
)

// blockPolicy keeps identity clustering deliberately small while admitting a
// complete body before a response can contribute a vote.
type blockPolicy struct {
	rpc.Policy
	byHash bool
}

func (p blockPolicy) RequiresAgreement() bool {
	if a, ok := p.Policy.(rpc.AgreementPolicy); ok {
		return a.RequiresAgreement()
	}
	return true
}

func (p blockPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if cp, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return cp.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}

// Admit validates only full transaction responses. Header-only calls are
// used to establish moving references and intentionally do not require body
// fields that the wire request did not ask the source to return.
func (p blockPolicy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if string(value) == "null" {
		return nil, nil
	}
	fullTx := len(req.Params) > 1 && req.Params[1] == true
	if !fullTx {
		return nil, nil
	}
	evidence, err := admitBlock(ctx, req, value, p.byHash, call)
	if err != nil {
		return nil, err
	}
	return evidence, nil
}

type blockWire struct {
	Hash         *common.Hash        `json:"hash"`
	Transactions []json.RawMessage   `json:"transactions"`
	UncleHashes  []common.Hash       `json:"uncles"`
	Withdrawals  []*types.Withdrawal `json:"withdrawals"`
}

func admitBlock(ctx context.Context, req rpc.Request, raw json.RawMessage, byHash bool, call rpc.AdmissionCaller) (json.RawMessage, error) {
	var head *types.Header
	if err := json.Unmarshal(raw, &head); err != nil || head == nil {
		if err == nil {
			err = fmt.Errorf("null block")
		}
		return nil, malformed("block header", err)
	}
	var body blockWire
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, malformed("block body", err)
	}
	if body.Hash == nil {
		return nil, malformed("full block missing hash", nil)
	}
	if *body.Hash != head.Hash() {
		return nil, malformed("wire hash disagrees with computed header hash", nil)
	}
	if err := bindBlockRequest(req, head, *body.Hash, byHash); err != nil {
		return nil, err
	}

	txs := make(types.Transactions, len(body.Transactions))
	for i, item := range body.Transactions {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] == '"' {
			return nil, malformed(fmt.Sprintf("transaction %d is hash-only", i), nil)
		}
		tx := new(types.Transaction)
		if err := json.Unmarshal(item, tx); err != nil {
			return nil, malformed(fmt.Sprintf("transaction %d", i), err)
		}
		txs[i] = tx
	}
	if got := types.DeriveSha(txs, trie.NewStackTrie(nil)); got != head.TxHash {
		return nil, malformed("transactions root mismatch", nil)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, malformed("block object", err)
	}
	for _, field := range []string{"transactions", "uncles"} {
		item, present := obj[field]
		if !present || string(item) == "null" {
			return nil, malformed("full block missing "+field, nil)
		}
	}
	withdrawalsPresent := obj["withdrawals"] != nil
	if head.WithdrawalsHash == nil {
		if withdrawalsPresent && string(obj["withdrawals"]) != "null" {
			return nil, malformed("withdrawals supplied before withdrawals fork", nil)
		}
	} else {
		if !withdrawalsPresent || string(obj["withdrawals"]) == "null" {
			return nil, malformed("withdrawals missing for header commitment", nil)
		}
		for i, withdrawal := range body.Withdrawals {
			if withdrawal == nil {
				return nil, malformed(fmt.Sprintf("withdrawal %d is null", i), nil)
			}
		}
		got := types.DeriveSha(types.Withdrawals(body.Withdrawals), trie.NewStackTrie(nil))
		if got != *head.WithdrawalsHash {
			return nil, malformed("withdrawals root mismatch", nil)
		}
	}

	if head.UncleHash == types.EmptyUncleHash {
		if len(body.UncleHashes) != 0 {
			return nil, malformed("uncle list is non-empty for empty uncle root", nil)
		}
		return nil, nil
	}
	if len(body.UncleHashes) == 0 {
		return nil, malformed("uncle list missing for non-empty uncle root", nil)
	}
	if call == nil {
		return nil, malformed("uncle loader unavailable", nil)
	}
	uncleEvidence := make([]json.RawMessage, len(body.UncleHashes))
	uncleHeaders := make([]*types.Header, len(body.UncleHashes))
	for i, expected := range body.UncleHashes {
		rawUncle, err := call(ctx, "eth_getUncleByBlockHashAndIndex", []any{body.Hash.Hex(), fmt.Sprintf("0x%x", i)})
		if err != nil {
			return nil, fmt.Errorf("uncle %d: %w", i, err)
		}
		if string(rawUncle) == "null" {
			return nil, malformed(fmt.Sprintf("uncle %d is null", i), nil)
		}
		var uncle *types.Header
		if err := json.Unmarshal(rawUncle, &uncle); err != nil || uncle == nil {
			return nil, malformed(fmt.Sprintf("uncle %d header", i), err)
		}
		if uncle.Hash() != expected {
			return nil, malformed(fmt.Sprintf("uncle %d hash mismatch", i), nil)
		}
		uncleHeaders[i] = uncle
		uncleEvidence[i] = append(json.RawMessage(nil), rawUncle...)
	}
	if got := types.CalcUncleHash(uncleHeaders); got != head.UncleHash {
		return nil, malformed("uncle root mismatch", nil)
	}
	return json.Marshal(uncleEvidence)
}

func bindBlockRequest(req rpc.Request, head *types.Header, hash common.Hash, byHash bool) error {
	if len(req.Params) == 0 {
		return fmt.Errorf("%w: block request has no reference", rpc.ErrInvalidRequest)
	}
	ref, ok := req.Params[0].(string)
	if !ok {
		return fmt.Errorf("%w: block reference is not a string", rpc.ErrInvalidRequest)
	}
	ref = strings.ToLower(ref)
	if byHash {
		if !common.IsHexHash(ref) || common.HexToHash(ref) != hash {
			return malformed("block hash does not match requested hash", nil)
		}
		return nil
	}
	if ref == "latest" || ref == "safe" || ref == "finalized" || ref == "pending" {
		return nil
	}
	if !strings.HasPrefix(ref, "0x") {
		return nil
	}
	var n big.Int
	if _, ok := n.SetString(strings.TrimPrefix(ref, "0x"), 16); !ok || head.Number == nil || n.Cmp(head.Number) != 0 {
		return malformed("block number does not match requested number", nil)
	}
	return nil
}

func malformed(what string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", rpc.ErrMalformedResponse, what)
	}
	return fmt.Errorf("%w: %s: %v", rpc.ErrMalformedResponse, what, cause)
}
