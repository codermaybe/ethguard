package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestJSONCheckedLogPreservesAdmissionAndAliasStages(t *testing.T) {
	for _, text := range []string{
		logBase() + `}`, logBase() + `,"Address":"0x` + strings.Repeat("77", 20) + `"}`,
		logBase() + `,"Data":"oops"}`, logBase() + `,"BlockTimestamp":"oops"}`,
		logBase() + `,"Topics":null}`, logBase() + `,"BlockTimestamp":null}`,
		strings.Replace(logBase(), `"address":`, `"ADDRESS":`, 1) + `}`,
		`{}`, `null`, `[]`, `1`,
	} {
		raw := json.RawMessage(text)
		original := bytes.Clone(raw)
		if err := jsonsafe.Validate(raw); err != nil {
			t.Fatal(err)
		}
		want, wantErr := EncodeOne(raw)
		got, gotErr := EncodeOneJSONChecked(raw)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("checked(%s) = %+v, %v; raw = %+v, %v", raw, got, gotErr, want, wantErr)
		}
		if !bytes.Equal(raw, original) {
			t.Fatal("checked encoder modified input")
		}
	}
}

func TestLogArraySafetyStillPrecedesItemSemantics(t *testing.T) {
	// The first row is semantically incomplete. A later duplicate must still
	// be rejected by the whole-array gate before any row is interpreted.
	raw := json.RawMessage(`[{},` + logBase() + `,"data":"0x"}]`)
	if _, err := Encode(raw); !errors.Is(err, jsonsafe.ErrDuplicateKey) || !strings.HasPrefix(err.Error(), "log JSON:") {
		t.Fatalf("whole-array duplicate gate = %v", err)
	}
	for _, depth := range []int{jsonsafe.MaxNestingDepth - 2, jsonsafe.MaxNestingDepth - 1} {
		row := json.RawMessage(logBase() + `,"vendor":` + strings.Repeat("[", depth) + `0` + strings.Repeat("]", depth) + `}`)
		if _, err := EncodeOne(row); err != nil {
			t.Fatalf("standalone row at depth %d: %v", depth, err)
		}
		array := append(append([]byte{'['}, row...), ']')
		_, err := Encode(array)
		if depth == jsonsafe.MaxNestingDepth-2 && err != nil {
			t.Fatalf("array exactly at depth budget: %v", err)
		}
		if depth == jsonsafe.MaxNestingDepth-1 && !errors.Is(err, jsonsafe.ErrTooDeep) {
			t.Fatalf("array lost enclosing depth: %v", err)
		}
	}
}
