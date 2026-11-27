package block

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

	"github.com/codermaybe/ethguard/internal/ethcodec"
	objectheader "github.com/codermaybe/ethguard/internal/ethobject/header"
	"github.com/codermaybe/ethguard/internal/ethobject/transaction"
	"github.com/codermaybe/ethguard/rpc"
)

type blockWire struct {
	Hash         *common.Hash        `json:"hash"`
	Transactions []json.RawMessage   `json:"transactions"`
	UncleHashes  []common.Hash       `json:"uncles"`
	Withdrawals  []*types.Withdrawal `json:"withdrawals"`
}

// FullBlock retains the complete canonical value and same-source uncle
// evidence produced by full-block admission. Its bytes are owned privately;
// methods consume evidence while content delivery requests a transaction view.
// The transaction view deliberately decodes the canonical Block representation,
// not the separate transaction candidates used to verify its trie commitment.
type FullBlock struct {
	canonical json.RawMessage
	uncles    json.RawMessage
}

// UncleEvidence returns an independent copy of the admitted uncle responses.
func (b *FullBlock) UncleEvidence() json.RawMessage {
	return append(json.RawMessage(nil), b.uncles...)
}

// Transactions parses the checked canonical Block's private payload view.
// This delivery step is separate from admission: methods that only consume
// evidence do not pay for it or acquire an additional failure stage.
func (b *FullBlock) Transactions() (types.Transactions, error) {
	var body struct {
		Transactions types.Transactions `json:"transactions"`
	}
	if err := json.Unmarshal(b.canonical, &body); err != nil {
		return nil, err
	}
	return body.Transactions, nil
}

// AdmitBlock validates the complete source-local value, request binding and
// body commitments. It retains the same Encode result used by the shape check
// so content delivery can consume it without encoding the original again.
func AdmitBlock(ctx context.Context, req rpc.Request, raw json.RawMessage, byHash bool, call rpc.AdmissionCaller) (*FullBlock, error) {
	head, err := objectheader.Decode(raw)
	if err != nil {
		return nil, malformed("block header", err)
	}
	// Validate the complete source-local official object before accepting any
	// payload commitment. Required metadata cannot be supplied by another source.
	canonicalBlock, err := Encode(raw)
	if err != nil {
		return nil, malformed("block shape", err)
	}
	// The generated contract canonicalizes only this source's declarations.
	// Algorithm-only geth body types consume those bytes, so valid JSON string
	// escapes cannot become a contradictory second wire grammar. Keep original
	// transaction objects separately for Candidate's declaration checks below.
	var body blockWire
	if err := json.Unmarshal(canonicalBlock, &body); err != nil {
		return nil, malformed("block body", err)
	}
	var original struct {
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &original); err != nil {
		return nil, malformed("block transactions", err)
	}
	body.Transactions = original.Transactions
	if err := validateBlockWireScalars(raw); err != nil {
		return nil, malformed("block scalar", err)
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
		candidate, err := transaction.Prepare(item)
		if err != nil {
			return nil, malformed(fmt.Sprintf("transaction %d", i), err)
		}
		// Candidate validates the source's signed payload and declarations
		// before its canonical bytes cross the private geth bridge. JSON
		// string escapes must not become a second, contradictory wire gate.
		if err := candidate.AdmitInBlock(rpc.Request{Params: []any{body.Hash.Hex(), fmt.Sprintf("0x%x", i)}}); err != nil {
			return nil, malformed(fmt.Sprintf("transaction %d", i), err)
		}
		canonical := candidate.Canonical()
		if err := bindTransaction(canonical, head, i); err != nil {
			return nil, malformed(fmt.Sprintf("transaction %d context", i), err)
		}
		tx := new(types.Transaction)
		if err := json.Unmarshal(canonical, tx); err != nil {
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

	// An empty uncle list is legal when the header's uncle root is empty,
	// including an empty post-merge block. A non-empty list against that
	// root is not. This is per-response integrity, not a completeness claim.
	if head.UncleHash == types.EmptyUncleHash {
		if len(body.UncleHashes) != 0 {
			return nil, malformed("uncle list is non-empty for empty uncle root", nil)
		}
		return &FullBlock{canonical: canonicalBlock}, nil
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
		uncle, err := objectheader.Decode(rawUncle)
		if err != nil {
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
	encodedEvidence, err := json.Marshal(uncleEvidence)
	if err != nil {
		return nil, err
	}
	return &FullBlock{canonical: canonicalBlock, uncles: encodedEvidence}, nil
}

// validateBlockWireScalars applies context-sensitive wire checks to fields
// that are present in a block response. Fork-specific optional fields remain
// optional; their presence still cannot bypass the scalar contract.
func validateBlockWireScalars(raw json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	for _, field := range []string{"hash", "parentHash", "sha3Uncles", "transactionsRoot", "stateRoot", "receiptsRoot", "withdrawalsRoot", "parentBeaconBlockRoot", "requestsHash"} {
		if value, ok := obj[field]; ok && !ethcodec.IsJSONNull(value) {
			if _, err := ethcodec.ValidateWireFixedHex(value, 64, field); err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
		}
	}
	for _, field := range []string{"number", "gasLimit", "gasUsed", "timestamp", "size", "baseFeePerGas", "blobGasUsed", "excessBlobGas"} {
		if value, ok := obj[field]; ok && !ethcodec.IsJSONNull(value) {
			if _, err := ethcodec.NormalizeWireQuantity(value); err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
		}
	}
	if value, ok := obj["logsBloom"]; ok && !ethcodec.IsJSONNull(value) {
		if _, err := ethcodec.ValidateWireFixedHex(value, 512, "logsBloom"); err != nil {
			return err
		}
	}
	if value, ok := obj["nonce"]; ok && !ethcodec.IsJSONNull(value) {
		if _, err := ethcodec.ValidateWireFixedHex(value, 16, "nonce"); err != nil {
			return err
		}
	}
	if value, ok := obj["extraData"]; ok && !ethcodec.IsJSONNull(value) {
		if _, err := ethcodec.NormalizeWireData(value); err != nil {
			return fmt.Errorf("extraData: %w", err)
		}
	}
	if value, ok := obj["miner"]; ok && !ethcodec.IsJSONNull(value) {
		if _, err := ethcodec.ValidateWireFixedHex(value, 40, "miner"); err != nil {
			return err
		}
	}
	return nil
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

// bindTransaction checks the original declarations against the block carrying
// them. Sender syntax and signed-payload hash are checked by the transaction Candidate;
// the sender remains a provider declaration rather than a recovered identity.
func bindTransaction(raw json.RawMessage, head *types.Header, index int) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	hash, err := ethcodec.ValidateWireFixedHex(fields["blockHash"], 64, "blockHash")
	if err != nil {
		return err
	}
	if common.HexToHash(hash) != head.Hash() {
		return fmt.Errorf("blockHash does not match containing block")
	}
	for _, item := range []struct {
		name string
		want string
	}{
		{"blockNumber", head.Number.String()}, {"blockTimestamp", fmt.Sprintf("%d", head.Time)}, {"transactionIndex", fmt.Sprintf("%d", index)},
	} {
		got, err := ethcodec.NormalizeWireQuantity(fields[item.name])
		if err != nil {
			return fmt.Errorf("%s: %w", item.name, err)
		}
		if got != item.want {
			return fmt.Errorf("%s does not match containing block", item.name)
		}
	}
	return nil
}
