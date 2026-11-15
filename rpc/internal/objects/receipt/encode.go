package receipt

import (
	"encoding/json"
	"github.com/codermaybe/ethguard/ethapi"
	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/ethereum/go-ethereum/common"
)

func Encode(value json.RawMessage) (json.RawMessage, error) { return objectreceipt.Encode(value) }
func EncodeBlock(value json.RawMessage) (json.RawMessage, error) {
	return objectreceipt.EncodeBlock(value)
}

type canonicalReceipt = ethapi.Receipt

var zeroHash = common.Hash{}.Hex()
