package reference

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
)

// HeadView is one source's decoded view of a chain head: the height and hash
// the reference selection works on, plus the original bytes so evidence keeps
// the source's own spelling.
type HeadView struct {
	Source rpc.SourceRef
	Height uint64
	Hash   string
	Raw    json.RawMessage
}

// HeadCodec turns raw head responses into views and comparison keys. It owns
// the head-shape contract for reference establishment: which responses count
// as decodable heads, and which bytes two heads are compared on. The
// production binding is supplied by the composition root, so this package
// stays free of method-policy imports; tests supply their own codec.
type HeadCodec interface {
	// Decode validates one raw head response and returns its height, hash
	// (lowercased), and original bytes. A response without a comparable
	// identity is a decode error, not a zero view.
	Decode(json.RawMessage) (HeadView, error)
	// Normalize reduces one raw head response to the comparison key head
	// clustering agrees on.
	Normalize(json.RawMessage) (string, error)
}
