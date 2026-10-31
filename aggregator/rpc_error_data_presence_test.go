package aggregator

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

type presenceProtocolError struct {
	code int
	msg  string
}

func (e presenceProtocolError) Error() string  { return e.msg }
func (e presenceProtocolError) ErrorCode() int { return e.code }

type decodedPresenceError struct {
	presenceProtocolError
	data any
}

func (e decodedPresenceError) ErrorData() any { return e.data }

type preservedPresenceError struct {
	presenceProtocolError
	raw     json.RawMessage
	present bool
}

func (e preservedPresenceError) ErrorDataJSON() (json.RawMessage, bool) {
	return append(json.RawMessage(nil), e.raw...), e.present
}

func TestObservationRPCErrorRecordsWireDataPresenceWithoutChangingRaw(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState rpc.ErrorDataPresenceState
		wantData  string
		wantRaw   string
	}{
		{
			name:      "captured missing member",
			err:       preservedPresenceError{presenceProtocolError: presenceProtocolError{code: 3, msg: "execution reverted"}},
			wantState: rpc.ErrorDataPresenceMissing,
			wantRaw:   `{"code":3,"message":"execution reverted"}`,
		},
		{
			name:      "captured null member",
			err:       preservedPresenceError{presenceProtocolError: presenceProtocolError{code: 3, msg: "execution reverted"}, raw: json.RawMessage("null"), present: true},
			wantState: rpc.ErrorDataPresenceNull,
			wantData:  "null",
			wantRaw:   `{"code":3,"message":"execution reverted","data":null}`,
		},
		{
			name:      "captured concrete member",
			err:       preservedPresenceError{presenceProtocolError: presenceProtocolError{code: 3, msg: "execution reverted"}, raw: json.RawMessage(`"0xdeadbeef"`), present: true},
			wantState: rpc.ErrorDataPresenceValue,
			wantData:  `"0xdeadbeef"`,
			wantRaw:   `{"code":3,"message":"execution reverted","data":"0xdeadbeef"}`,
		},
		{
			name:      "decoded geth nil is unknowable",
			err:       decodedPresenceError{presenceProtocolError: presenceProtocolError{code: 3, msg: "execution reverted"}},
			wantState: rpc.ErrorDataPresenceUnknown,
			wantRaw:   `{"code":3,"message":"execution reverted"}`,
		},
		{
			name:      "decoded geth value remains value",
			err:       decodedPresenceError{presenceProtocolError: presenceProtocolError{code: 3, msg: "execution reverted"}, data: "0xdeadbeef"},
			wantState: rpc.ErrorDataPresenceValue,
			wantData:  `"0xdeadbeef"`,
			wantRaw:   `{"code":3,"message":"execution reverted","data":"0xdeadbeef"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ObservationRPCError(tc.err)
			if got == nil {
				t.Fatal("ObservationRPCError returned nil")
			}
			if got.DataPresence != tc.wantState {
				t.Fatalf("DataPresence = %s, want %s", got.DataPresence, tc.wantState)
			}
			if string(got.Data) != tc.wantData {
				t.Fatalf("Data = %q, want %q", got.Data, tc.wantData)
			}
			if string(got.Raw()) != tc.wantRaw {
				t.Fatalf("Raw() = %s, want %s", got.Raw(), tc.wantRaw)
			}
		})
	}
}
