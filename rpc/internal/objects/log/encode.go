package log

import (
	"encoding/json"
	objectlog "github.com/codermaybe/ethguard/internal/ethobject/log"
)

// CanonicalLog is the shared protected log representation.
type CanonicalLog = objectlog.CanonicalLog

func Encode(value json.RawMessage) (json.RawMessage, error) { return objectlog.Encode(value) }
func EncodeOne(value json.RawMessage) (CanonicalLog, error) { return objectlog.EncodeOne(value) }
