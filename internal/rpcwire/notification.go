package rpcwire

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrMalformedNotification reports an invalid Ethereum subscription
// notification. It is a wire validation failure, not a provider RPC error.
var ErrMalformedNotification = errors.New("rpcwire: malformed subscription notification")

// SubscriptionNotification is one validated eth_subscription notification.
// Result owns its bytes independently of the input body. Explicit JSON null
// is a present result; the notification's consumer interprets its value.
type SubscriptionNotification struct {
	Method         string
	SubscriptionID string
	Result         json.RawMessage
}

// DecodeSubscriptionNotification validates one JSON-RPC 2.0 Ethereum
// subscription notification. Notifications have no id, use eth_subscription,
// and carry a nonempty subscription ID and a present result in params.
// Unknown members are tolerated, as in the response codec. Version is the
// decoded string value "2.0", including equivalent JSON escapes. Decoding
// retains the existing encoding/json member matching and duplicate-key
// behavior; a final null version is invalid even after a valid string.
//
// Every failure wraps ErrMalformedNotification. No response-only result/error
// rules or provider error interpretation apply to notification params.
func DecodeSubscriptionNotification(body []byte) (SubscriptionNotification, error) {
	var envelope struct {
		Version json.RawMessage `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return SubscriptionNotification{}, malformedNotificationf("body is not a JSON object: %v", err)
	}
	var version string
	if err := json.Unmarshal(envelope.Version, &version); err != nil || version != Version {
		return SubscriptionNotification{}, malformedNotificationf("unsupported or missing jsonrpc version %s", envelope.Version)
	}
	if envelope.ID != nil {
		return SubscriptionNotification{}, malformedNotificationf("notification must not carry an id")
	}
	if envelope.Method != "eth_subscription" {
		return SubscriptionNotification{}, malformedNotificationf("unsupported notification method %q", envelope.Method)
	}
	var params struct {
		Subscription string          `json:"subscription"`
		Result       json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return SubscriptionNotification{}, malformedNotificationf("params is not a JSON object: %v", err)
	}
	if params.Subscription == "" || params.Result == nil {
		return SubscriptionNotification{}, malformedNotificationf("missing subscription or result")
	}
	return SubscriptionNotification{
		Method:         envelope.Method,
		SubscriptionID: params.Subscription,
		Result:         params.Result,
	}, nil
}

func malformedNotificationf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformedNotification, fmt.Sprintf(format, args...))
}
