package rpc

// ErrorDataPresenceState describes how much of a JSON-RPC error's data
// member the calling adapter could establish for this invocation. It is a
// diagnostic fact only: resolver comparison keys continue to use RPCError.Raw
// and never use this state.
//
// Unknown means the adapter exposed only the decoded DataError view, where a
// missing member and an explicit JSON null both become nil. This is the
// expected state for the current geth WebSocket path.
type ErrorDataPresenceState uint8

const (
	// ErrorDataPresenceUnknown means the wire presence could not be observed.
	ErrorDataPresenceUnknown ErrorDataPresenceState = iota
	// ErrorDataPresenceMissing means the error object omitted its data member.
	ErrorDataPresenceMissing
	// ErrorDataPresenceNull means the error object contained data: null.
	ErrorDataPresenceNull
	// ErrorDataPresenceValue means the error object contained a non-null data
	// value, including an empty string or a provider-specific JSON value.
	ErrorDataPresenceValue
)

func (s ErrorDataPresenceState) String() string {
	switch s {
	case ErrorDataPresenceUnknown:
		return "unknown"
	case ErrorDataPresenceMissing:
		return "missing"
	case ErrorDataPresenceNull:
		return "null"
	case ErrorDataPresenceValue:
		return "value"
	default:
		return "invalid"
	}
}
