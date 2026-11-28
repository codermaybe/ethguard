package rpcwire

import (
	"math"
	"testing"
)

func TestRequestCapacityChecksEveryAddition(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		headerLen, paramsLen int
		want                 int
		wantErr              bool
	}{
		{name: "ordinary", headerLen: 42, paramsLen: 2, want: 54},
		{name: "prefix reaches max", headerLen: math.MaxInt - 10, want: math.MaxInt},
		{name: "prefix exceeds max", headerLen: math.MaxInt - 9, wantErr: true},
		{name: "header already max", headerLen: math.MaxInt, wantErr: true},
		{name: "params reach max", headerLen: 42, paramsLen: math.MaxInt - 52, want: math.MaxInt},
		{name: "params exceed max", headerLen: 42, paramsLen: math.MaxInt - 51, wantErr: true},
		{name: "second addition exceeds max", headerLen: math.MaxInt - 10, paramsLen: 1, wantErr: true},
		{name: "both max", headerLen: math.MaxInt, paramsLen: math.MaxInt, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requestCapacity(tc.headerLen, tc.paramsLen)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("capacity(%d, %d) = (%d, %v), want (%d, error=%v)", tc.headerLen, tc.paramsLen, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
