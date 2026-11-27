package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestResponseCannotInventAnAnswer(t *testing.T) {
	cases := []Response{
		{},
		{Exchange: Exchange{Progress: ExchangeCompleted}},
		{Exchange: Exchange{Progress: ExchangeStarted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`null`)}},
		{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult}},
		{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerMissing}},
		{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`null`), Cause: context.Canceled}},
	}
	for _, response := range cases {
		if response.Err() == nil {
			t.Fatalf("invalid response reports success: %+v", response)
		}
	}
	if !errors.Is((Response{}).Err(), ErrNoResponse) {
		t.Fatal("zero response must have no-response classification")
	}
}

func TestResponseCancellationRetainsCompletedNull(t *testing.T) {
	response := Response{Exchange: Exchange{Progress: ExchangeCompleted, HTTPStatus: 200, Cause: context.Canceled}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`null`)}}
	if !errors.Is(response.Err(), context.Canceled) {
		t.Fatal(response.Err())
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if string(response.Answer.Result) != "null" {
		t.Fatal("cancellation erased answer")
	}
}

func TestResponseCloneAndDecodeHaveIndependentOwnership(t *testing.T) {
	response := Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`{"value":1}`)}}
	clone := response.Clone()
	var decoded json.RawMessage
	if err := response.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	clone.Answer.Result[0] = '!'
	decoded[0] = '?'
	if string(response.Answer.Result) != `{"value":1}` {
		t.Fatal("result bytes aliased another owner")
	}
	var wrong int
	if err := response.Decode(&wrong); err == nil {
		t.Fatal("object decoded into int")
	}
	if response.Err() != nil || response.Answer.Kind != AnswerResult {
		t.Fatal("local decode changed provider facts")
	}
}

type responseDataError struct {
	data    json.RawMessage
	present bool
}

func (e *responseDataError) Error() string                          { return "rejected" }
func (e *responseDataError) ErrorCode() int                         { return -32000 }
func (e *responseDataError) ErrorDataJSON() (json.RawMessage, bool) { return e.data, e.present }

func TestProtocolAnswerSnapshotsBorrowedDataAndPreservesCause(t *testing.T) {
	cause := &responseDataError{data: json.RawMessage(`{"reason":1}`), present: true}
	response := Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: ProtocolAnswer(cause)}
	clone := response.Clone()
	cause.data[0] = '!'
	clone.Answer.RPCError.Data[0] = '?'
	if string(response.Answer.RPCError.Data) != `{"reason":1}` {
		t.Fatal("protocol data was not snapshotted")
	}
	if !errors.Is(response.Err(), cause) {
		t.Fatal("original protocol error lost")
	}
	var same *responseDataError
	if !errors.As(response.Err(), &same) || same != cause {
		t.Fatal("error capability lost")
	}
}

func TestProtocolAnswerDistinguishesMissingAndNullData(t *testing.T) {
	for _, tc := range []struct {
		present bool
		data    string
		want    ErrorDataPresenceState
	}{
		{false, "", ErrorDataPresenceMissing}, {true, "null", ErrorDataPresenceNull}, {true, `"x"`, ErrorDataPresenceValue},
	} {
		answer := ProtocolAnswer(&responseDataError{data: json.RawMessage(tc.data), present: tc.present})
		if answer.RPCError.DataPresence != tc.want {
			t.Fatalf("data %s/%v: %+v", tc.data, tc.present, answer.RPCError)
		}
	}
}

func TestResponseMethodJudgmentRetainsProtocolFacts(t *testing.T) {
	cause := errors.New("method rejected result")
	response := Response{Exchange: Exchange{Progress: ExchangeCompleted, HTTPStatus: 200}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`{"value":1}`)}}
	for _, state := range []InterpretationState{InterpretationRejected, InterpretationExcluded, InterpretationInternalFailure} {
		response.Answer.Interpretation = Interpretation{State: state, Cause: cause}
		clone := response.Clone()
		if clone.Answer.Interpretation != response.Answer.Interpretation || clone.Exchange != response.Exchange {
			t.Fatalf("clone lost method/communication facts: %+v", clone)
		}
		clone.Answer.Result[0] = '!'
		if string(response.Answer.Result) != `{"value":1}` || response.Answer.Kind != AnswerResult {
			t.Fatal("method judgment changed the original result")
		}
		if _, err := response.Candidate(false); !errors.Is(err, cause) {
			t.Fatalf("rejected candidate error = %v", err)
		}
	}
	protocol := &responseDataError{data: json.RawMessage(`null`), present: true}
	response.Answer = ProtocolAnswer(protocol)
	if response.CandidateErr(true) != nil || !errors.Is(response.CandidateErr(false), protocol) {
		t.Fatal("protocol opt-in did not control eligibility")
	}
	response.Answer.Interpretation = Interpretation{State: InterpretationRejected, Cause: cause}
	if !errors.Is(response.Err(), cause) || response.Answer.Kind != AnswerProtocolError || response.Answer.Cause != protocol || response.Answer.RPCError.DataPresence != ErrorDataPresenceNull { //nolint:errorlint // The answer must retain the original protocol error instance.
		t.Fatal("method error erased the protocol error or its data presence")
	}
}

func TestResponseRejectsImpossibleMethodJudgments(t *testing.T) {
	cause := errors.New("method failure")
	result := Answer{Kind: AnswerResult, Result: json.RawMessage(`null`)}
	for _, response := range []Response{
		{Exchange: Exchange{Progress: ExchangeNotStarted, Cause: context.Canceled}, Answer: Answer{Interpretation: Interpretation{State: InterpretationAdmitted}}},
		{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerMissing, Cause: ErrNoResponse, Interpretation: Interpretation{State: InterpretationRejected, Cause: cause}}},
		{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerProtocolError, RPCError: &RPCError{Code: 3}, Cause: errors.New("fake protocol error")}},
	} {
		if !errors.Is(response.Validate(), ErrInvalidResponseState) {
			t.Fatalf("accepted impossible method/protocol facts: %+v", response)
		}
	}
	for _, state := range []InterpretationState{InterpretationRejected, InterpretationExcluded, InterpretationInternalFailure} {
		result.Interpretation = Interpretation{State: state}
		if (Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: result}).Validate() == nil {
			t.Fatalf("state %v accepted without a cause", state)
		}
	}
	result.Interpretation = Interpretation{State: InterpretationAdmitted, Cause: cause}
	if (Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: result}).Validate() == nil {
		t.Fatal("admitted result accepted with failure cause")
	}
}

type decodedResponseError struct {
	reads int
	data  any
}

func (e *decodedResponseError) Error() string  { return "decoded protocol error" }
func (e *decodedResponseError) ErrorCode() int { return -32000 }
func (e *decodedResponseError) ErrorData() any { e.reads++; return e.data }

func TestProtocolAnswerSnapshotsDecodedDataOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     any
		presence ErrorDataPresenceState
		want     string
	}{
		{"unknown", nil, ErrorDataPresenceUnknown, ""},
		{"value", map[string]int{"reason": 1}, ErrorDataPresenceValue, `{"reason":1}`},
		{"unencodable", make(chan int), ErrorDataPresenceUnknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := &decodedResponseError{data: tc.data}
			response := Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: ProtocolAnswer(cause)}
			if cause.reads != 1 || response.Answer.RPCError.DataPresence != tc.presence || string(response.Answer.RPCError.Data) != tc.want {
				t.Fatalf("snapshot reads=%d payload=%+v", cause.reads, response.Answer.RPCError)
			}
			var original *decodedResponseError
			if !errors.As(response.Err(), &original) || original != cause {
				t.Fatal("original decoded-data capability lost")
			}
		})
	}
}
