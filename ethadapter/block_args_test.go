package ethadapter

import (
	"errors"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard"
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
	// -(2^64+2) sits outside int64; a truncated Int64() would alias the
	// "latest" tag (-2), so it must be rejected before any switch on it.
	beyondInt64 := new(big.Int).Sub(
		new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 64)),
		big.NewInt(2))
	for _, n := range []*big.Int{big.NewInt(-6), beyondInt64} {
		if _, err := checkedBlockNumArg(n); err == nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("unknown negative %v: got %v", n, err)
		}
		if got := toBlockNumArg(n); got != "" {
			t.Fatalf("toBlockNumArg(%v) = %q, want the empty rejection marker", n, got)
		}
	}
}
