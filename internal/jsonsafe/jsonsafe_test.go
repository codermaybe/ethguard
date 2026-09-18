package jsonsafe

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidateRejectsDuplicateKeysAtEachObjectScope(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"outer":{"a":1,"a":2}}`} {
		if err := Validate([]byte(raw)); !errors.Is(err, ErrDuplicateKey) {
			t.Fatalf("Validate(%s) error = %v, want duplicate key", raw, err)
		}
	}
}

func TestValidateRejectsDepthAndObjectKeyLimits(t *testing.T) {
	deep := strings.Repeat("[", MaxNestingDepth+1) + "null" + strings.Repeat("]", MaxNestingDepth+1)
	if err := Validate([]byte(deep)); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("deep JSON error = %v, want depth error", err)
	}

	var many strings.Builder
	many.WriteByte('{')
	for i := 0; i <= MaxObjectKeys; i++ {
		if i != 0 {
			many.WriteByte(',')
		}
		many.WriteString(`"k`)
		many.WriteString(strconv.Itoa(i))
		many.WriteString(`":1`)
	}
	many.WriteByte('}')
	if err := Validate([]byte(many.String())); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("large object error = %v, want key limit", err)
	}
}

func TestValidateRejectsTrailingDataAndDecodeUsesStandardTypes(t *testing.T) {
	if err := Validate([]byte(`null null`)); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("trailing data error = %v", err)
	}
	var value struct {
		N int `json:"n"`
	}
	if err := Decode([]byte(`{"n":7}`), &value); err != nil || value.N != 7 {
		t.Fatalf("Decode() = value %v, error %v", value, err)
	}
}

func TestValidateRejectsBodyOverLimit(t *testing.T) {
	raw := make([]byte, MaxBodyBytes+1)
	for i := range raw {
		raw[i] = ' '
	}
	if err := Validate(raw); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized body error = %v, want body limit", err)
	}
}
