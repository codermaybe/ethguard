package ethadapter

import (
	"errors"
	"math/big"
	"testing"

	"ethguard"
)

func TestBlockNumberArgumentMatchesGethTags(t *testing.T) {
	cases := []struct {
		n    *big.Int
		want string
	}{
		{nil, "latest"},
		{big.NewInt(-1), "pending"},
		{big.NewInt(-2), "latest"},
		{big.NewInt(-3), "finalized"},
		{big.NewInt(-4), "safe"},
		{big.NewInt(-5), "0x0"},
		{big.NewInt(16), "0x10"},
	}
	for _, tc := range cases {
		if got := toBlockNumArg(tc.n); got != tc.want {
			t.Errorf("toBlockNumArg(%v) = %q, want %q", tc.n, got, tc.want)
		}
		if got, err := checkedBlockNumArg(tc.n); err != nil || got != tc.want {
			t.Errorf("checkedBlockNumArg(%v) = %q, %v; want %q", tc.n, got, err, tc.want)
		}
	}
}

func TestBlockNumberArgumentRejectsUnknownNegative(t *testing.T) {
	if _, err := checkedBlockNumArg(big.NewInt(-6)); err == nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("unknown negative: got %v", err)
	}
}
