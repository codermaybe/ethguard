package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
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
				response := rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.ProtocolAnswer(err)}
				answer := writeAnswer(source, &response)
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
				second := writeAnswer(source, &response)
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
			response := rpc.Response{Exchange: rpc.Exchange{Cause: tc.err}}
			if tc.value != "" {
				response.Exchange.Progress = rpc.ExchangeCompleted
				response.Answer = rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(tc.value)}
			}
			got := writeAnswer(source, &response)
			if got.Source != source || got.Hash != tc.hash || got.Rejection != nil {
				t.Fatalf("answer=%+v, want hash %q without rejection", got, tc.hash)
			}
		})
	}
}

func TestWritePreservesMissingAndCanceledAnswers(t *testing.T) {
	hash := "0x" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name     string
		response rpc.Response
		kind     rpc.AnswerKind
		reason   rpc.DecisionReason
		want     error
	}{
		{"empty", rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerMissing, Cause: rpc.ErrNoResponse}}, rpc.AnswerMissing, rpc.ReasonUnassessed, rpc.ErrNoValidResponse},
		{"invalid envelope", rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerInvalid, Cause: rpc.ErrMalformedResponse}}, rpc.AnswerInvalid, rpc.ReasonNoCandidate, rpc.ErrMalformedResponse},
		{"completed result then canceled", rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted, Cause: context.Canceled}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"` + hash + `"`)}}, rpc.AnswerResult, rpc.ReasonUnassessed, rpc.ErrNoValidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := writeAnswer(rpc.SourceRef{ID: "a"}, &tc.response)
			answer.Attempted = true
			decision, err := rpcengine.ResolveWriteBroadcast(1, rpcengine.Corroboration{Floor: 1}, hash, []rpc.SourceAnswer{answer}, nil)
			if !errors.Is(err, tc.want) || decision.Adjudication.Reason != tc.reason || decision.Status != rpc.WriteUnconfirmed {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
			got := decision.Sources[0]
			if got.Answer.Kind != tc.kind || got.Answer.Interpretation.State != rpc.InterpretationUnassessed || got.Exchange != tc.response.Exchange {
				t.Fatalf("facts changed: %+v", got)
			}
		})
	}
}
