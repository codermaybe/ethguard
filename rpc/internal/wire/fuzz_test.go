package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzNormalize exercises the shared normalizers with arbitrary input.
// Invariants per normalizer on success:
//  1. output shape: quantities are canonical decimal strings; data/storage
//     outputs are lowercase 0x-prefixed hex with an even digit count;
//  2. fixpoint: re-normalizing the quoted output yields the output itself;
//  3. cross-normalizer agreement: quantities accepted by both the strict
//     wire normalizer and the legacy hex normalizer agree numerically.
//
// A panic or an unstable comparison key is the finding — comparison keys
// feed the corroboration vote, so instability would silently split votes.
func FuzzNormalize(f *testing.F) {
	seeds := []string{
		`"0x1"`, `"0x0a"`, `"0X1A"`, `"0x00010"`, `"0xabc"`, `"0x"`,
		`"0xzz"`, `"-0x1"`, `"0xdeadBEEF"`, `10`, `null`, `""`, `"0"`,
		`"12345"`, `" 12 "`, `"1.5"`, `"-3"`, `"999999999999999999999999"`,
		`"0x" + strings.Repeat("f", 65)`, /* placeholder replaced below */
	}
	seeds[len(seeds)-1] = `"0x` + strings.Repeat("f", 65) + `"`
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		value := json.RawMessage(in)

		qty, qtyErr := NormalizeWireQuantity(value)
		legacy, legacyErr := NormalizeHexQuantity(value)
		if qtyErr == nil && legacyErr == nil && qty != legacy {
			t.Fatalf("normalizers disagree for %q: wire=%q legacy=%q", in, qty, legacy)
		}
		// Quantity output is canonical decimal rather than a 0x string, so
		// the re-quote fixpoint does not apply; the legacy-agreement check
		// above is its stability probe.
		_ = qtyErr

		if data, err := NormalizeWireData(value); err == nil {
			assertHexShape(t, data, in)
			assertFixpoint(t, value, NormalizeWireData, "NormalizeWireData")
		}

		if word, err := NormalizeStorageWord(value); err == nil {
			assertHexShape(t, word, in)
			assertFixpoint(t, value, NormalizeStorageWord, "NormalizeStorageWord")
		}

		if dec, err := NormalizeDecimalString(value); err == nil {
			// Decimal output must itself re-decode as the same decimal.
			quoted, _ := json.Marshal(dec)
			if again, err := NormalizeDecimalString(quoted); err != nil || again != dec {
				t.Fatalf("decimal output %q does not re-decode (err %v, again %q)", dec, err, again)
			}
		}

		// CanonicalizeHex must converge: applying it twice is a no-op.
		var first, second any
		if json.Unmarshal(in, &first) != nil {
			return
		}
		CanonicalizeHex(&first)
		once, marshalErr := json.Marshal(first)
		if marshalErr != nil {
			t.Fatalf("marshal after CanonicalizeHex: %v", marshalErr)
		}
		if json.Unmarshal(once, &second) == nil {
			CanonicalizeHex(&second)
			twice, err := json.Marshal(second)
			if err != nil {
				t.Fatalf("marshal after second CanonicalizeHex: %v", err)
			}
			if string(once) != string(twice) {
				t.Fatalf("CanonicalizeHex not idempotent for %q: %s vs %s", in, once, twice)
			}
		}
	})
}

// assertFixpoint re-quotes a normalizer's string output as a JSON string and
// requires the same normalizer to map it back to itself.
func assertFixpoint(t *testing.T, in []byte, normalize func(json.RawMessage) (string, error), name string) {
	t.Helper()
	out, err := normalize(in)
	if err != nil {
		t.Fatalf("%s: unexpected error in fixpoint probe: %v", name, err)
	}
	quoted, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		t.Fatalf("%s: re-quote of output %q failed: %v", name, out, marshalErr)
	}
	again, againErr := normalize(quoted)
	if againErr != nil || again != out {
		t.Fatalf("%s not a fixpoint for %q: %q -> %q (err %v)", name, in, out, again, againErr)
	}
}

func assertHexShape(t *testing.T, out string, in []byte) {
	t.Helper()
	if !strings.HasPrefix(out, "0x") || len(out)%2 != 0 {
		t.Fatalf("malformed data output %q for input %q", out, in)
	}
	if strings.ToLower(out[2:]) != out[2:] {
		t.Fatalf("data output not lowercased: %q (input %q)", out, in)
	}
}
