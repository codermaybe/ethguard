package ethguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

type writeDecodedError struct{ data any }

func (writeDecodedError) Error() string    { return "already known" }
func (writeDecodedError) ErrorCode() int   { return -32601 }
func (e writeDecodedError) ErrorData() any { return e.data }

type writePreservedError struct {
	writeDecodedError
	raw     json.RawMessage
	present bool
}

// Deliberately return borrowed bytes: the write answer must own its evidence.
func (e writePreservedError) ErrorDataJSON() (json.RawMessage, bool) { return e.raw, e.present }

func TestWriteAnswerPreservesErrorData(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		presence rpc.ErrorDataPresenceState
		data     string
	}{
		{"missing", writePreservedError{}, rpc.ErrorDataPresenceMissing, ""},
		{"null", writePreservedError{raw: json.RawMessage(" null "), present: true}, rpc.ErrorDataPresenceNull, " null "},
		{"empty string", writePreservedError{raw: json.RawMessage(`""`), present: true}, rpc.ErrorDataPresenceValue, `""`},
		{"empty bytes", writePreservedError{raw: json.RawMessage(`"0x"`), present: true}, rpc.ErrorDataPresenceValue, `"0x"`},
		{"raw takes precedence", writePreservedError{writeDecodedError: writeDecodedError{data: float64(9007199254740993)}, raw: json.RawMessage(`{ "n": 9007199254740993, "v": [1e+09, 123456789012345678901234567890] }`), present: true}, rpc.ErrorDataPresenceValue, `{ "n": 9007199254740993, "v": [1e+09, 123456789012345678901234567890] }`},
		{"decoded nil", writeDecodedError{}, rpc.ErrorDataPresenceUnknown, ""},
		{"decoded value", writeDecodedError{data: "0xdead"}, rpc.ErrorDataPresenceValue, `"0xdead"`},
	} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", tc.name, wrapped), func(t *testing.T) {
				err := tc.err
				if wrapped {
					err = fmt.Errorf("source context: %w", err)
				}
				source := rpc.SourceRef{Name: "source"}
				answer := writeAnswerClassified(source, json.RawMessage(`"0x`+strings.Repeat("ab", 32)+`"`), err)
				if answer.Source != source || answer.Hash != "" || answer.Rejection == nil {
					t.Fatalf("protocol error misclassified: %+v", answer)
				}
				rejection := answer.Rejection
				if rejection.Code != -32601 || rejection.Message != "already known" {
					t.Fatalf("protocol fields changed: %+v", rejection)
				}
				if rejection.DataPresence != tc.presence {
					t.Errorf("presence=%s, want %s", rejection.DataPresence, tc.presence)
				}
				if string(rejection.Data) != tc.data {
					t.Errorf("data=%q, want %q", rejection.Data, tc.data)
				}
				if t.Failed() {
					return
				}
				second := writeAnswerClassified(source, nil, err)
				if len(rejection.Data) > 0 {
					rejection.Data[0] = '!'
					if string(second.Rejection.Data) != tc.data {
						t.Fatal("answers share data storage")
					}
					var preserved rpc.ErrorDataPresence
					if errors.As(err, &preserved) {
						raw, _ := preserved.ErrorDataJSON()
						if string(raw) != tc.data {
							t.Fatal("answer mutated source error data")
						}
					}
				}
			})
		}
	}
}

func TestWriteAnswerClassification(t *testing.T) {
	hash := "0x" + strings.Repeat("AB", 32)
	for _, tc := range []struct {
		name, value string
		err         error
		hash        string
	}{
		{"hash", `"` + hash + `"`, nil, strings.ToLower(hash)},
		{"invalid result", `"already known"`, nil, ""},
		{"null result", `null`, nil, ""},
		{"silence", "", nil, ""},
		{"transport error", `"` + hash + `"`, errors.New("already known"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := rpc.SourceRef{Name: "source"}
			got := writeAnswerClassified(source, json.RawMessage(tc.value), tc.err)
			if got.Source != source || got.Hash != tc.hash || got.Rejection != nil {
				t.Fatalf("answer=%+v, want hash %q without rejection", got, tc.hash)
			}
		})
	}
}
