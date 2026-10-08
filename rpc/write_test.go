package rpc

import (
	"errors"
	"testing"
)

func writeSource(id string) SourceRef { return SourceRef{ID: id, Name: id} }

func writeHash(id, hash string) SourceAnswer {
	return SourceAnswer{Source: writeSource(id), Hash: hash}
}

func writeReject(id string) SourceAnswer {
	return SourceAnswer{
		Source:    writeSource(id),
		Rejection: &RPCError{Code: -32010, Message: "nonce too low"},
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
		name          string
		basis         int
		floor         int
		answers       []SourceAnswer
		wantStatus    WriteStatus
		wantErr       error
		wantHash      string
		wantAgreement int
		wantDissent   int
		wantRejected  int
		wantSilent    int
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
			wantStatus: WriteDisputed, wantErr: ErrConflict, wantHash: "0xaaa", wantAgreement: 1, wantDissent: 1, wantRejected: 1,
		},
		{
			name:  "rejected",
			basis: 3, floor: 2,
			answers:    []SourceAnswer{writeReject("a"), writeReject("b"), writeReject("c")},
			wantStatus: WriteRejected, wantErr: ErrNoValidResponse, wantRejected: 3,
		},
		{
			name:  "unreachable",
			basis: 3, floor: 2,
			answers: []SourceAnswer{
				{Source: writeSource("a")},
				{Source: writeSource("b")},
				{Source: writeSource("c")},
			},
			wantStatus: WriteUnreachable, wantErr: ErrNoValidResponse, wantSilent: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := ResolveWriteBroadcast(tt.basis, tt.floor, tt.answers, nil)
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
			if decision.Hash != tt.wantHash || decision.Agreement != tt.wantAgreement {
				t.Fatalf("winner = %q/%d, want %q/%d", decision.Hash, decision.Agreement, tt.wantHash, tt.wantAgreement)
			}
			if len(decision.Dissent) != tt.wantDissent {
				t.Fatalf("dissent = %d, want %d: %+v", len(decision.Dissent), tt.wantDissent, decision.Dissent)
			}
			if len(decision.Rejected) != tt.wantRejected {
				t.Fatalf("rejected = %d, want %d", len(decision.Rejected), tt.wantRejected)
			}
			if len(decision.Silent) != tt.wantSilent {
				t.Fatalf("silent = %d, want %d", len(decision.Silent), tt.wantSilent)
			}
		})
	}
}

func TestResolveWriteBroadcastPreservesFaults(t *testing.T) {
	fault := errors.New("transport timeout")
	decision, err := ResolveWriteBroadcast(2, 2, []SourceAnswer{
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
