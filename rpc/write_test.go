package rpc

import (
	"errors"
	"testing"
)

func writeSource(id string) SourceRef { return SourceRef{ID: id, Name: id} }

func writeHash(id, hash string) SourceAnswer {
	return SourceAnswer{SourceSummary: SourceSummary{Source: writeSource(id), Attempted: true, Exchange: Exchange{Progress: ExchangeCompleted}, Answer: AnswerSummary{Kind: AnswerResult, Interpretation: Interpretation{State: InterpretationValidated}}}, Hash: hash}
}

func writeReject(id string) SourceAnswer {
	return SourceAnswer{
		SourceSummary: SourceSummary{Source: writeSource(id), Attempted: true, Exchange: Exchange{Progress: ExchangeCompleted}, Answer: AnswerSummary{Kind: AnswerProtocolError, Cause: writeProtocolCause{}, Interpretation: Interpretation{State: InterpretationValidated}}},
		Rejection:     &RPCError{Code: -32010, Message: "nonce too low"},
	}
}

func TestWriteQuorumUsesFixedBasisAndFloor(t *testing.T) {
	tests := []struct {
		basis int
		floor int
		want  int
	}{
		{basis: 1, floor: 0, want: 1},
		{basis: 2, floor: 0, want: 2},
		{basis: 3, floor: 0, want: 2},
		{basis: 4, floor: 0, want: 3},
		{basis: 5, floor: 4, want: 4},
		{basis: 5, floor: 8, want: 8},
	}
	for _, tt := range tests {
		if got := WriteQuorum(tt.basis, tt.floor); got != tt.want {
			t.Fatalf("WriteQuorum(%d, %d) = %d, want %d", tt.basis, tt.floor, got, tt.want)
		}
	}
}

func TestWriteStatusZeroValueIsUnknown(t *testing.T) {
	if (WriteStatus(0)).String() != "unknown" || WriteUnknown != 0 {
		t.Fatalf("zero write status = %d/%q, want unknown", WriteUnknown, WriteStatus(0).String())
	}
}

func TestResolveWriteBroadcastStatusMatrix(t *testing.T) {
	tests := []struct {
		name            string
		basis           int
		floor           int
		answers         []SourceAnswer
		wantStatus      WriteStatus
		wantErr         error
		wantHash        string
		wantAgreement   int
		wantDissent     int
		wantRejected    int
		wantUnavailable int
	}{
		{
			name:  "accepted with minority dissent",
			basis: 3, floor: 2,
			answers: []SourceAnswer{
				writeHash("a", "0xaaa"),
				writeHash("b", "0xaaa"),
				writeHash("c", "0xbbb"),
			},
			wantStatus: WriteAccepted, wantHash: "0xaaa", wantAgreement: 2, wantDissent: 1,
		},
		{
			name:  "disputed below quorum",
			basis: 3, floor: 2,
			answers: []SourceAnswer{
				writeHash("a", "0xaaa"),
				writeHash("b", "0xbbb"),
				writeReject("c"),
			},
			wantStatus: WriteUnconfirmed, wantErr: ErrConflict, wantHash: "0xaaa", wantAgreement: 1, wantDissent: 1, wantRejected: 1,
		},
		{
			name:  "rejected",
			basis: 3, floor: 2,
			answers:    []SourceAnswer{writeReject("a"), writeReject("b"), writeReject("c")},
			wantStatus: WriteUnconfirmed, wantErr: ErrNoValidResponse, wantRejected: 3,
		},
		{
			name:  "unreachable",
			basis: 3, floor: 2,
			answers: []SourceAnswer{
				{SourceSummary: SourceSummary{Source: writeSource("a"), Attempted: true, Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
				{SourceSummary: SourceSummary{Source: writeSource("b"), Attempted: true, Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
				{SourceSummary: SourceSummary{Source: writeSource("c"), Attempted: true, Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
			},
			wantStatus: WriteUnconfirmed, wantErr: ErrNoValidResponse, wantUnavailable: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := ResolveWriteBroadcast(tt.basis, Corroboration{Floor: tt.floor}, "0xaaa", tt.answers, nil)
			if decision.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s; decision = %+v", decision.Status, tt.wantStatus, decision)
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, tt.wantErr)
			}
			if decision.Hash != tt.wantHash || len(writeRelations(decision.Sources, VerdictSupported)) != tt.wantAgreement {
				t.Fatalf("winner = %q/%d, want %q/%d", decision.Hash, len(writeRelations(decision.Sources, VerdictSupported)), tt.wantHash, tt.wantAgreement)
			}
			if len(writeRelations(decision.Sources, VerdictDissented)) != tt.wantDissent {
				t.Fatalf("dissent = %d, want %d: %+v", len(writeRelations(decision.Sources, VerdictDissented)), tt.wantDissent, writeRelations(decision.Sources, VerdictDissented))
			}
			if len(writeRejections(decision.Sources)) != tt.wantRejected {
				t.Fatalf("rejected = %d, want %d", len(writeRejections(decision.Sources)), tt.wantRejected)
			}
			if len(writeUnavailable(decision.Sources)) != tt.wantUnavailable {
				t.Fatalf("silent = %d, want %d", len(writeUnavailable(decision.Sources)), tt.wantUnavailable)
			}
		})
	}
}

func TestResolveWriteBroadcastPreservesFaults(t *testing.T) {
	fault := errors.New("transport timeout")
	decision, err := ResolveWriteBroadcast(2, Corroboration{Floor: 2}, "0xaaa", []SourceAnswer{
		writeHash("a", "0xaaa"),
		writeHash("b", "0xaaa"),
	}, []error{fault})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != WriteAccepted || len(decision.Faults) != 1 || !errors.Is(decision.Faults[0], fault) {
		t.Fatalf("fault evidence lost: %+v", decision)
	}
}

// These assertion helpers query the authoritative source evidence without
// storing a second classification in the decision.
func writeRelations(sources []SourceAnswer, relation VerdictKind) []SourceAnswer {
	var matches []SourceAnswer
	for _, source := range sources {
		if source.Relation == relation {
			matches = append(matches, source)
		}
	}
	return matches
}
func writeRejections(sources []SourceAnswer) []SourceAnswer {
	var matches []SourceAnswer
	for _, source := range sources {
		if source.Rejection != nil {
			matches = append(matches, source)
		}
	}
	return matches
}
func writeUnavailable(sources []SourceAnswer) []SourceAnswer {
	var matches []SourceAnswer
	for _, source := range sources {
		if source.Answer.Kind == AnswerUnassessed {
			matches = append(matches, source)
		}
	}
	return matches
}

type writeProtocolCause struct{}

func (writeProtocolCause) Error() string  { return "nonce too low" }
func (writeProtocolCause) ErrorCode() int { return -32010 }

// The public resolver accepts interpreted summaries, so it must reject
// impossible summaries even when no hash/rejection payload was supplied.
func TestWriteRejectsImpossibleSourceSummaries(t *testing.T) {
	for name, mutate := range map[string]func(*SourceAnswer){
		"unknown exchange":          func(a *SourceAnswer) { a.Exchange.Progress = ExchangeProgress(99); a.Hash = "" },
		"unknown answer":            func(a *SourceAnswer) { a.Answer.Kind = AnswerKind(99); a.Hash = "" },
		"unknown method":            func(a *SourceAnswer) { a.Answer.Interpretation.State = InterpretationState(99); a.Hash = "" },
		"result before completion":  func(a *SourceAnswer) { a.Exchange.Progress = ExchangeResponded; a.Hash = "" },
		"method without answer":     func(a *SourceAnswer) { a.Answer.Kind = AnswerUnassessed; a.Hash = "" },
		"rejected without cause":    func(a *SourceAnswer) { a.Answer.Interpretation.State = InterpretationRejected; a.Hash = "" },
		"validated without product": func(a *SourceAnswer) { a.Hash = "" },
		"unattempted completion":    func(a *SourceAnswer) { a.Attempted = false },
		"hash and rejection":        func(a *SourceAnswer) { a.Rejection = &RPCError{Code: -32010} },
		"hash after cancellation":   func(a *SourceAnswer) { a.Exchange.Cause = errors.New("canceled") },
		"fake protocol cause":       func(a *SourceAnswer) { *a = writeReject("a"); a.Answer.Cause = errors.New("not a protocol error") },
		"mismatched protocol code":  func(a *SourceAnswer) { *a = writeReject("a"); a.Rejection.Code = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			answer := writeHash("a", "0xaaa")
			mutate(&answer)
			decision, err := ResolveWriteBroadcast(1, Corroboration{}, "0xaaa", []SourceAnswer{answer}, nil)
			if !errors.Is(err, ErrInvalidOutcome) || decision.Status == WriteAccepted || decision.Adjudication.Reason != ReasonInternalFailure {
				t.Fatalf("impossible summary accepted: %+v, %v", decision, err)
			}
		})
	}
	if _, err := ResolveWriteBroadcast(1, Corroboration{}, "", []SourceAnswer{writeHash("a", "0xaaa")}, nil); !errors.Is(err, ErrInvalidOutcome) {
		t.Fatalf("missing submitted hash accepted: %v", err)
	}
}

func TestWriteForwardKeepsFailedAndUnvisitedMembers(t *testing.T) {
	cause := errors.New("dial failed")
	answers := []SourceAnswer{
		{SourceSummary: SourceSummary{Source: writeSource("a"), Attempted: true, Exchange: Exchange{Cause: cause}}},
		writeHash("b", "0xaaa"),
		{SourceSummary: SourceSummary{Source: writeSource("c")}},
	}
	decision, err := ResolveWriteBroadcast(3, Corroboration{Mode: CorroborationForwarded, Floor: 3}, "0xaaa", answers, nil)
	if err != nil || decision.Status != WriteAccepted || decision.Adjudication.Basis != 3 || decision.Adjudication.Required != 1 {
		t.Fatalf("forward bound = %+v, %v", decision, err)
	}
	if !decision.Sources[0].Attempted || !errors.Is(decision.Sources[0].Exchange.Cause, cause) || decision.Sources[0].Relation != VerdictUnusable || decision.Sources[2].Attempted || decision.Sources[2].Relation != VerdictAbsent {
		t.Fatalf("forward lost scheduling/communication: %+v", decision.Sources)
	}
}
