package ethapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

// Freeze Text's constructor separately from constrained hex scalars: arbitrary
// Unicode, controls encoded as escapes and replacement characters are valid.
func referenceText(raw json.RawMessage) (ethapi.Text, error) {
	if len(raw) == 0 || bytes.Equal(bytes.Trim(raw, " \t\r\n"), []byte("null")) {
		return "", fmt.Errorf("Text: expected a non-null value")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("expected string: %w", err)
	}
	return ethapi.Text(text), nil
}

func TestTextConstructorMatchesStandardJSON(t *testing.T) {
	inputs := []json.RawMessage{
		nil, []byte("null"), []byte(" \tnull\r\n"), []byte(" "),
		[]byte("false"), []byte("0"), []byte("[]"), []byte("{}"),
		[]byte(`""`), []byte(`"plain / <>&"`), []byte(`"中文🙂"`),
		[]byte(`"\u0000\ud800\ud83d\ude42"`), []byte(`"escaped\ntext"`),
		[]byte("\t\"plain\"\r\n"), []byte(`"one" "two"`), []byte(`"one" null`),
		[]byte(`"\q"`), []byte(`"unfinished`),
		[]byte(`"` + strings.Repeat("a", 64<<10) + "\x00\""),
	}
	for c := 0; c <= 255; c++ {
		inputs = append(inputs, []byte{'"', 'a', byte(c), 'z', '"'})
	}
	for _, raw := range inputs {
		want, wantErr := referenceText(raw)
		got, gotErr := ethapi.NewText(raw)
		if got != want || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("NewText (%d bytes) = %q, %T %v; standard = %q, %T %v", len(raw), got, gotErr, gotErr, want, wantErr, wantErr)
		}
	}
}

func TestNativeStringConstructorsOwnInput(t *testing.T) {
	check := func(t *testing.T, raw json.RawMessage, parse func(json.RawMessage) (string, error)) {
		t.Helper()
		got, err := parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Clone(got)
		for i := range raw {
			raw[i] = '!'
		}
		if got != want {
			t.Fatalf("constructor result aliases raw: got %q, want %q", got, want)
		}
	}
	for _, constructor := range strictScalarConstructors {
		t.Run(constructor.name, func(t *testing.T) {
			check(t, []byte(`"`+constructor.sample+`"`), constructor.parse)
		})
	}
	for _, constructor := range fixedHexConstructors {
		t.Run(constructor.name, func(t *testing.T) {
			check(t, []byte(`"0x`+strings.Repeat("a", constructor.digits)+`"`), constructor.parse)
		})
	}
	t.Run("Text", func(t *testing.T) {
		check(t, []byte(`"plain text"`), func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewText(raw)
			return string(value), err
		})
	})
}
