package livecanary

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	filterInvalidURL  = "invalid_url"
	filterNonHTTPS    = "non_https"
	filterCredentials = "credentials"
	filterDuplicate   = "duplicate"
	filterCapExceeded = "cap_exceeded"
)

func validFilterReason(reason string) bool {
	switch reason {
	case filterInvalidURL, filterNonHTTPS, filterCredentials, filterDuplicate, filterCapExceeded:
		return true
	default:
		return false
	}
}

type registryChain struct {
	ChainID json.RawMessage `json:"chainId"`
	RPC     json.RawMessage `json:"rpc"`
}

type candidate struct {
	index    int
	url      string
	hasURL   bool
	reason   string
	identity string
}

// Select applies chainlist-sorted-v1 to the target chain. Registry entries for
// other chains are ignored, including malformed ones; only the target chain's
// RPC list can make selection fail.
func Select(chainID uint64, raw []byte) (*Selection, error) {
	selection, _, err := SelectWithTarget(chainID, raw)
	return selection, err
}

// SelectWithTarget also reports whether the target chain was present. T10.2
// needs that distinction: a missing target chain is registry_unavailable,
// while a present chain with too few valid members is insufficient_members.
func SelectWithTarget(chainID uint64, raw []byte) (*Selection, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false, fmt.Errorf("registry: top-level JSON must be an array")
	}
	var chains []json.RawMessage
	if err := json.Unmarshal(trimmed, &chains); err != nil {
		return nil, false, fmt.Errorf("registry: parse: %w", err)
	}
	var entries []json.RawMessage
	found := false
	for i, chain := range chains {
		var parsed registryChain
		if err := json.Unmarshal(chain, &parsed); err != nil {
			continue
		}
		id, ok := parseChainID(parsed.ChainID)
		if !ok || id != chainID {
			continue
		}
		found = true
		if len(parsed.RPC) == 0 || bytes.Equal(bytes.TrimSpace(parsed.RPC), []byte("null")) {
			return nil, false, fmt.Errorf("registry: chain %d entry %d: missing rpc list", chainID, i)
		}
		var rpc []json.RawMessage
		if err := json.Unmarshal(parsed.RPC, &rpc); err != nil {
			return nil, false, fmt.Errorf("registry: chain %d entry %d: rpc: %w", chainID, i, err)
		}
		entries = append(entries, rpc...)
	}
	return selectCandidates(entries), found, nil
}

func parseChainID(raw json.RawMessage) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		id, err := strconv.ParseUint(number.String(), 10, 64)
		return id, err == nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		id, err := strconv.ParseUint(text, 10, 64)
		return id, err == nil
	}
	return 0, false
}

func selectCandidates(entries []json.RawMessage) *Selection {
	candidates := make([]candidate, len(entries))
	seen := make(map[string]bool, len(entries))
	var legal []*candidate
	for i, raw := range entries {
		current := &candidates[i]
		current.index = i
		current.url, current.hasURL = candidateURL(raw)

		if !current.hasURL {
			current.reason = filterInvalidURL
			continue
		}
		parsed, err := url.Parse(current.url)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || strings.ContainsRune(current.url, '#') {
			current.reason = filterInvalidURL
			continue
		}
		if !strings.EqualFold(parsed.Scheme, "https") {
			current.reason = filterNonHTTPS
			continue
		}
		if parsed.User != nil || queryHasSensitiveName(parsed.RawQuery) {
			current.reason = filterCredentials
			continue
		}
		identity, err := EndpointIdentity(current.url)
		if err != nil {
			current.reason = filterInvalidURL
			continue
		}
		if seen[identity] {
			current.reason = filterDuplicate
			continue
		}
		seen[identity] = true
		current.identity = identity
		legal = append(legal, current)
	}

	sort.Slice(legal, func(i, j int) bool {
		return legal[i].identity < legal[j].identity
	})
	selectedCount := len(legal)
	if selectedCount > MaxMembers {
		selectedCount = MaxMembers
	}
	for _, item := range legal[selectedCount:] {
		item.reason = filterCapExceeded
	}

	selection := &Selection{
		Algorithm:      SelectionAlgorithm,
		MinMembers:     MinMembers,
		MaxMembers:     MaxMembers,
		CandidateCount: len(candidates),
		Selected:       make([]SelectedMember, 0, selectedCount),
		Filtered:       make([]FilteredEntry, 0, len(candidates)-selectedCount),
	}
	for i := 0; i < selectedCount; i++ {
		selection.Selected = append(selection.Selected, SelectedMember{
			SourceID: fmt.Sprintf("node-%02d", i+1),
			URL:      legal[i].url,
		})
	}
	for i := range candidates {
		item := &candidates[i]
		if item.reason == "" {
			continue
		}
		hash := sha256Hex([]byte(item.url))
		if !item.hasURL {
			hash = sha256Hex(entries[item.index])
		}
		selection.Filtered = append(selection.Filtered, FilteredEntry{
			URLSHA256: hash,
			Reason:    item.reason,
		})
	}
	selection.VotingBasisSHA256 = VotingBasisSHA256(selection.Selected)
	return selection
}

func candidateURL(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, text != ""
	}
	var object struct {
		URL json.RawMessage `json:"url"`
	}
	if err := json.Unmarshal(raw, &object); err == nil && len(object.URL) > 0 {
		var value string
		if err := json.Unmarshal(object.URL, &value); err == nil && value != "" {
			return value, true
		}
	}
	return "", false
}

// EndpointIdentity mirrors pool's endpoint deduplication key. Selection must
// deduplicate exactly as the production pool does.
func EndpointIdentity(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("url must include scheme and host")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	if isDefaultPort(parsed.Scheme, parsed.Port()) {
		parsed.Host = parsed.Hostname()
		if strings.Contains(parsed.Host, ":") {
			parsed.Host = "[" + parsed.Host + "]"
		}
	}
	return parsed.String(), nil
}

func isDefaultPort(scheme, port string) bool {
	switch scheme {
	case "http", "ws":
		return port == "80"
	case "https", "wss":
		return port == "443"
	default:
		return false
	}
}

func VotingBasisSHA256(members []SelectedMember) string {
	hash := sha256.New()
	for _, member := range members {
		hash.Write([]byte(member.SourceID))
		hash.Write([]byte{'\t'})
		hash.Write([]byte(member.URL))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validateHTTPSURL(field, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return fmt.Errorf("%s: must be an absolute HTTPS URL", field)
	}
	return nil
}

func validateSelectedURL(field, raw string) error {
	if err := validateHTTPSURL(field, raw); err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if strings.ContainsRune(raw, '#') {
		return fmt.Errorf("%s: must not contain a fragment", field)
	}
	if parsed.User != nil || queryHasSensitiveName(parsed.RawQuery) {
		return fmt.Errorf("%s: must not contain credentials", field)
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
