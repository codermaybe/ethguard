package ethcodec

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestIsLowerFixedHexMatchesRegex(t *testing.T) {
	for _, width := range []int{0, 1, 16, 64, 512} {
		pattern := regexp.MustCompile(fmt.Sprintf(`^0x[0-9a-f]{%d}$`, width))
		inputs := []string{"", "0", "0x", "0X", "0x\xff", "0xａ", "\n0x", "0x\n", "0x" + strings.Repeat("a", width), "0x" + strings.Repeat("a", width+1)}
		if width > 0 {
			inputs = append(inputs, "0x"+strings.Repeat("a", width-1))
			for c := 0; c <= 255; c++ {
				inputs = append(inputs, "0x"+string([]byte{byte(c)})+strings.Repeat("a", width-1))
			}
		}
		for _, text := range inputs {
			if got, want := IsLowerFixedHex(text, width), pattern.MatchString(text); got != want {
				t.Fatalf("IsLowerFixedHex(%q, %d) = %v, regexp = %v", text, width, got, want)
			}
		}
	}
	for _, width := range []int{-1, int(^uint(0) >> 1)} {
		if IsLowerFixedHex("0x", width) {
			t.Fatalf("invalid or oversized width %d accepted", width)
		}
	}
}
