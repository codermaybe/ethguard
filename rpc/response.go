package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ExchangeProgress is the furthest progress observed for this request. Started
// means local execution began; it promises neither delivery nor remote execution.
type ExchangeProgress uint8

const (
	ExchangeNotStarted ExchangeProgress = iota
	ExchangeStarted
	ExchangeResponded
	ExchangeCompleted
)

// Exchange records communication facts independently of the answer. Cause is
// the terminal exchange error (including local cancellation). HTTPStatus is zero
// when no HTTP status was observed. Completed does not imply a successful status.
type Exchange struct {
	Progress   ExchangeProgress
	HTTPStatus int
	Cause      error
}

// AnswerKind describes protocol interpretation only. Method admission must
// retain this answer and record its own interpretation separately.
type AnswerKind uint8

const (
	AnswerUnassessed AnswerKind = iota
	AnswerMissing
	AnswerInvalid
	AnswerResult
	AnswerProtocolError
)

// InterpretationState describes method processing, independently of the
// protocol answer. Admitted only means the method's admission check ran and
// passed; normalization/encoding may still reject it during resolution.
type InterpretationState uint8

const (
	InterpretationUnassessed InterpretationState = iota
	InterpretationAdmitted
	InterpretationRejected
	// InterpretationExcluded is a valid answer outside a local candidate selection.
	InterpretationExcluded
	InterpretationInternalFailure
	// InterpretationValidated means the resolver completed its method checks.
	InterpretationValidated
)

// Interpretation records the method owner's judgment. Unassessed may carry a
// cause when processing was interrupted; that is not provider rejection.
// Cause is immutable shared evidence; it never replaces protocol error data.
type Interpretation struct {
	State InterpretationState
	Cause error
}

// Answer owns its result/error data. Result is valid JSON, including literal
// null, only for AnswerResult. Cause retains the original decoding or protocol
// error so errors.Is/As and error-data capabilities remain available.
// Exported bytes are borrowed for reading: use Clone before mutating or handing
// them to an independent mutable owner. Reading fields never copies a payload.
type Answer struct {
	Interpretation Interpretation
	Kind           AnswerKind
	Result         json.RawMessage
	RPCError       *RPCError
	Cause          error
}

// Response is the authoritative pair of exchange and protocol-answer facts.
// Err and Decode are consumer projections; neither changes those facts.
// Producers transfer ownership of payload bytes into Response. Copying the
// struct shares those bytes; Clone provides independent payload ownership.
type Response struct {
	Exchange Exchange
	Answer   Answer
}

var (
	ErrNoResponse           = errors.New("rpc: no response formed")
	ErrInvalidResponseState = errors.New("rpc: invalid response state")
)

// Validate checks structural invariants without decoding the payload again.
// JSON validity is established once by the protocol decoder, not by every read.
func (r Response) Validate() error {
	invalid := func() error { return ErrInvalidResponseState }
	x, a := r.Exchange, r.Answer
	if err := validateResponseFacts(x, a.Kind, a.Interpretation); err != nil {
		return err
	}
	switch a.Kind {
	case AnswerUnassessed:
		if a.Result != nil || a.RPCError != nil || a.Cause != nil {
			return invalid()
		}
		if x.Cause == nil {
			return ErrNoResponse
		}
	case AnswerMissing, AnswerInvalid:
		if a.Result != nil || a.RPCError != nil || a.Cause == nil {
			return invalid()
		}
	case AnswerResult:
		if len(a.Result) == 0 || a.RPCError != nil || a.Cause != nil {
			return invalid()
		}
	case AnswerProtocolError:
		var protocol ProtocolError
		if a.Result != nil || a.RPCError == nil || !errors.As(a.Cause, &protocol) || protocol.ErrorCode() != a.RPCError.Code {
			return invalid()
		}
	}
	return nil
}

// validateResponseFacts checks the two domains without requiring raw result
// bytes. Public summaries use exactly the same state constraints as Response.
func validateResponseFacts(x Exchange, kind AnswerKind, method Interpretation) error {
	invalid := func() error { return ErrInvalidResponseState }
	if method.State > InterpretationValidated {
		return invalid()
	}
	if method.State != InterpretationUnassessed || method.Cause != nil {
		if kind != AnswerResult && kind != AnswerProtocolError {
			return invalid()
		}
	}
	if (method.State == InterpretationAdmitted || method.State == InterpretationValidated) && method.Cause != nil {
		return invalid()
	}
	if (method.State == InterpretationRejected || method.State == InterpretationExcluded || method.State == InterpretationInternalFailure) && method.Cause == nil {
		return invalid()
	}
	if x.Progress > ExchangeCompleted || kind > AnswerProtocolError || x.HTTPStatus < 0 || x.HTTPStatus > 999 {
		return invalid()
	}
	if x.HTTPStatus != 0 && (x.HTTPStatus < 100 || x.Progress < ExchangeResponded) {
		return invalid()
	}
	if kind != AnswerUnassessed && x.Progress != ExchangeCompleted {
		return invalid()
	}
	return nil
}

// Err projects the final error with exchange termination taking precedence over
// answer interpretation, while retaining both layers in the Response. A zero
// Response is never success.
func (r Response) Err() error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Exchange.Cause != nil {
		return r.Exchange.Cause
	}
	if r.Answer.Interpretation.Cause != nil {
		return r.Answer.Interpretation.Cause
	}
	return r.Answer.Cause
}

// Candidate selects material for a resolver's declared value domain. A protocol
// error is a candidate only when explicitly included; transport termination or
// method refusal always prevents selection. It never infers facts from Err().
// Result bytes are borrowed; protocol bodies are encoded once per selection.
func (r Response) Candidate(includeProtocolError bool) (json.RawMessage, error) {
	if err := r.CandidateErr(includeProtocolError); err != nil {
		return nil, err
	}
	if r.Answer.Kind == AnswerProtocolError {
		return r.Answer.RPCError.Raw(), nil
	}
	return r.Answer.Result, nil
}

// CandidateErr checks whether the existing answer can enter method resolution.
// Protocol-error eligibility is explicit and does not serialize its payload.
// It checks admission only; a resolver may still reject normalization/encoding.
func (r Response) CandidateErr(includeProtocolError bool) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Exchange.Cause != nil {
		return r.Exchange.Cause
	}
	if r.Answer.Interpretation.Cause != nil {
		return r.Answer.Interpretation.Cause
	}
	if r.Answer.Kind == AnswerResult || (includeProtocolError && r.Answer.Kind == AnswerProtocolError) {
		return nil
	}
	return r.Answer.Cause
}

// Clone copies result and protocol-error data once. Errors are immutable shared
// causes, as elsewhere in rpc evidence; callers must not mutate their internals.
func (r Response) Clone() Response {
	r.Answer.Result = append(json.RawMessage(nil), r.Answer.Result...)
	r.Answer.RPCError = r.Answer.RPCError.Clone()
	return r
}

// Decode projects a successful result into a caller-owned JSON target. A nil
// target discards the result; *json.RawMessage receives one independent copy.
// Local target/decode errors never rewrite the answer as a provider failure.
func (r Response) Decode(target any) error {
	if err := r.Err(); err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	if raw, ok := target.(*json.RawMessage); ok {
		if raw == nil {
			return fmt.Errorf("rpc: nil result target")
		}
		*raw = append((*raw)[:0], r.Answer.Result...)
		return nil
	}
	if err := json.Unmarshal(r.Answer.Result, target); err != nil {
		return fmt.Errorf("rpc: decode result: %w", err)
	}
	return nil
}

// ProtocolAnswer retains the original protocol error and snapshots its public
// payload. Unknown data presence (e.g. a test oracle) remains unknown.
// The input must implement ProtocolError; Validate rejects other inputs.
func ProtocolAnswer(err error) Answer {
	a := Answer{Kind: AnswerProtocolError, Cause: err}
	var protocol ProtocolError
	if !errors.As(err, &protocol) {
		return a
	}
	e := &RPCError{Code: protocol.ErrorCode(), Message: protocol.Error()}
	var presence ErrorDataPresence
	if errors.As(err, &presence) {
		raw, present := presence.ErrorDataJSON()
		e.Data = append(json.RawMessage(nil), raw...)
		switch {
		case !present:
			e.DataPresence = ErrorDataPresenceMissing
		case bytes.Equal(bytes.TrimSpace(raw), []byte("null")):
			e.DataPresence = ErrorDataPresenceNull
		default:
			e.DataPresence = ErrorDataPresenceValue
		}
	} else {
		var data DataError
		if errors.As(err, &data) {
			if decoded := data.ErrorData(); decoded != nil {
				if raw, marshalErr := json.Marshal(decoded); marshalErr == nil {
					e.Data = raw
					e.DataPresence = ErrorDataPresenceValue
				}
			}
		}
	}
	a.RPCError = e
	return a
}
