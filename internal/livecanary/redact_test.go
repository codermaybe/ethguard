package livecanary

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactRegistryWithoutCredentialsIsByteIdentical(t *testing.T) {
	raw := []byte(`[{"chainId":1,"rpc":["https://plain.example"]}]`)
	artifact, mode, err := redactRegistry(raw)
	if err != nil {
		t.Fatalf("redactRegistry: %v", err)
	}
	if mode != RedactionNone {
		t.Errorf("mode = %q, want %q", mode, RedactionNone)
	}
	if !bytes.Equal(artifact, raw) {
		t.Errorf("artifact changed without credentials:\nraw: %s\nartifact: %s", raw, artifact)
	}
}

func TestRedactRegistryRemovesCredentials(t *testing.T) {
	raw := readTestdata(t, "registry.json")
	artifact, mode, err := redactRegistry(raw)
	if err != nil {
		t.Fatalf("redactRegistry: %v", err)
	}
	if mode != RedactionURLCredentials {
		t.Fatalf("mode = %q, want %q", mode, RedactionURLCredentials)
	}
	text := string(artifact)
	for _, secret := range []string{"user:secret", "SECRET"} {
		if strings.Contains(text, secret) {
			t.Errorf("artifact still contains %q", secret)
		}
	}
	for _, want := range []string{"redacted@auth.example", "apikey=REDACTED", "token=REDACTED"} {
		if !strings.Contains(text, want) {
			t.Errorf("artifact missing %q:\n%s", want, text)
		}
	}
	replayed, replayMode, err := redactRegistry(artifact)
	if err != nil {
		t.Fatalf("redactRegistry(artifact): %v", err)
	}
	if replayMode != RedactionNone {
		t.Errorf("second redaction changed the artifact again")
	}
	if !bytes.Equal(replayed, artifact) {
		t.Errorf("redaction is not idempotent")
	}
}

func TestRedactString(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		changed bool
	}{
		{"https://user:pass@host/rpc", "https://redacted@host/rpc", true},
		{"https://user:pass@host/rpc#fragment", "https://redacted@host/rpc#fragment", true},
		{"https://host/rpc?api_key=abc&x=1", "https://host/rpc?api_key=REDACTED&x=1", true},
		{"https://host/rpc?KEY=abc", "https://host/rpc?KEY=REDACTED", true},
		{"https://host/rpc?apikey", "https://host/rpc?apikey=REDACTED", true},
		{"https://host/rpc?x=1", "https://host/rpc?x=1", false},
		{"https://redacted@host/rpc", "https://redacted@host/rpc", false},
		{"https://host/rpc?token=REDACTED", "https://host/rpc?token=REDACTED", false},
		{"not a url", "not a url", false},
		{"https://host/path@literal", "https://host/path@literal", false},
	}
	for _, test := range tests {
		got, changed := redactString(test.in)
		if got != test.want || changed != test.changed {
			t.Errorf("redactString(%q) = (%q, %v), want (%q, %v)",
				test.in, got, changed, test.want, test.changed)
		}
	}
}

func TestQueryHasSensitiveName(t *testing.T) {
	for _, query := range []string{
		"apikey=1",
		"API_KEY=1",
		"key=1",
		"token=1",
		"x=1&token=2",
		"api%6Bey=1",
	} {
		if !queryHasSensitiveName(query) {
			t.Errorf("queryHasSensitiveName(%q) = false, want true", query)
		}
	}
	for _, query := range []string{"", "x=1", "tracking=none", "keychain=1"} {
		if queryHasSensitiveName(query) {
			t.Errorf("queryHasSensitiveName(%q) = true, want false", query)
		}
	}
}
