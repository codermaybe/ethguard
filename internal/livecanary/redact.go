package livecanary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

var sensitiveQueryNames = map[string]bool{
	"apikey":  true,
	"api_key": true,
	"key":     true,
	"token":   true,
}

// redactRegistry publishes a safe copy of the fetched registry. When no
// credential-bearing string changes, the raw bytes are returned untouched so
// raw_sha256 and artifact_sha256 stay equal and comparable.
func redactRegistry(raw []byte) ([]byte, string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var document any
	if err := dec.Decode(&document); err != nil {
		return nil, "", fmt.Errorf("registry: decode for redaction: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, "", fmt.Errorf("registry: trailing data before redaction")
	}
	redacted, changed := redactValue(document)
	if !changed {
		return raw, RedactionNone, nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(redacted); err != nil {
		return nil, "", fmt.Errorf("registry: encode redacted copy: %w", err)
	}
	return buf.Bytes(), RedactionURLCredentials, nil
}

func redactValue(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		return redactString(typed)
	case []any:
		changed := false
		for i, item := range typed {
			updated, itemChanged := redactValue(item)
			if itemChanged {
				typed[i] = updated
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		changed := false
		for key, item := range typed {
			updated, itemChanged := redactValue(item)
			if itemChanged {
				typed[key] = updated
				changed = true
			}
		}
		return typed, changed
	default:
		return value, false
	}
}

// redactString removes URL userinfo and credential-named query values. It
// avoids URL round-tripping so malformed-but-obvious credential strings are
// still scrubbed without publishing the secret.
func redactString(value string) (string, bool) {
	out, changed := redactUserinfo(value)
	if queryRedacted, queryChanged := redactQuery(out); queryChanged {
		out = queryRedacted
		changed = true
	}
	return out, changed
}

func redactUserinfo(value string) (string, bool) {
	scheme := strings.Index(value, "://")
	if scheme < 0 {
		return value, false
	}
	start := scheme + 3
	end := len(value)
	for i := start; i < len(value); i++ {
		switch value[i] {
		case '/', '?', '#':
			end = i
			i = len(value)
		}
	}
	authority := value[start:end]
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return value, false
	}
	if authority[:at] == "redacted" {
		return value, false
	}
	return value[:start] + "redacted@" + authority[at+1:] + value[end:], true
}

func redactQuery(value string) (string, bool) {
	question := strings.IndexByte(value, '?')
	if question < 0 {
		return value, false
	}
	end := len(value)
	if fragment := strings.IndexByte(value[question+1:], '#'); fragment >= 0 {
		end = question + 1 + fragment
	}
	parts := strings.Split(value[question+1:end], "&")
	changed := false
	for i, part := range parts {
		if !sensitiveRawQueryName(part) {
			continue
		}
		name := part
		if equals := strings.IndexByte(part, '='); equals >= 0 {
			name = part[:equals]
		}
		replacement := name + "=REDACTED"
		if parts[i] != replacement {
			parts[i] = replacement
			changed = true
		}
	}
	if !changed {
		return value, false
	}
	return value[:question+1] + strings.Join(parts, "&") + value[end:], true
}

func queryHasSensitiveName(rawQuery string) bool {
	for _, part := range strings.Split(rawQuery, "&") {
		if sensitiveRawQueryName(part) {
			return true
		}
	}
	return false
}

func sensitiveRawQueryName(pair string) bool {
	name := pair
	if equals := strings.IndexByte(pair, '='); equals >= 0 {
		name = pair[:equals]
	}
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	return sensitiveQueryNames[strings.ToLower(name)]
}
