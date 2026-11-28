package ipc

import (
	"math"
	"testing"
)

func TestWriteMessageSizeIncludesNewlineWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		payloadLen int
		want       int
		wantErr    bool
	}{
		{name: "empty", want: 1},
		{name: "ordinary", payloadLen: 2, want: 3},
		{name: "reaches max", payloadLen: math.MaxInt - 1, want: math.MaxInt},
		{name: "exceeds max", payloadLen: math.MaxInt, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := writeMessageSize(tc.payloadLen)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("size(%d) = (%d, %v), want (%d, error=%v)", tc.payloadLen, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
