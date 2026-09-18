package methods

import (
	"encoding/json"

	"ethguard/rpc/internal/wire"
)

// MaskNode remains an alias for compatibility with the offline spec tests.
type MaskNode = wire.MaskNode

func Prune(data any, mask *MaskNode) any         { return wire.Prune(data, mask) }
func methodMasks() (map[string]*MaskNode, error) { return wire.MethodMasks() }
func PruneMethod(method string, raw json.RawMessage) (string, error) {
	return wire.PruneMethod(method, raw)
}
