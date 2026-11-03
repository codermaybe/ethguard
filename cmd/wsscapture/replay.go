package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

var protocolStates = []string{"missing", "null", "empty-hex", "value"}

func replayDirectory(dir string, out io.Writer) error {
	index, records, err := loadCapture(dir)
	if err != nil {
		return err
	}
	states := map[string]int{}
	keys := map[string]map[string]bool{}
	chainChecks := 0
	callChecks := 0
	wantChain := `"0x` + strconv.FormatUint(index.ChainID, 16) + `"`
	for _, record := range records {
		if !isProtocolState(record.DataState) {
			if record.Method == "eth_chainId" && record.DataState == "no-error" {
				if strings.TrimSpace(record.Result) != wantChain {
					return fmt.Errorf("%s/%s: eth_chainId result = %s, want %s", record.Provider, record.Scenario, record.Result, wantChain)
				}
				chainChecks++
			}
			continue
		}
		response, err := rpcwire.DecodeResponse([]byte(record.ResponseFrame), json.RawMessage(strconv.Itoa(record.RequestID)))
		if err != nil {
			return fmt.Errorf("%s/%s: decode captured frame: %w", record.Provider, record.Scenario, err)
		}
		if response.Error == nil {
			return fmt.Errorf("%s/%s: captured error frame decoded as a success", record.Provider, record.Scenario)
		}
		presence, ok := any(response.Error).(rpc.ErrorDataPresence)
		if !ok {
			return fmt.Errorf("%s/%s: decoded error has no ErrorDataPresence capability", record.Provider, record.Scenario)
		}
		state, err := stateFromPresence(presence)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", record.Provider, record.Scenario, err)
		}
		if state != record.DataState {
			return fmt.Errorf("%s/%s: captured state %s, decoded %s", record.Provider, record.Scenario, record.DataState, state)
		}
		structured := aggregator.ObservationRPCError(response.Error)
		if structured == nil {
			return fmt.Errorf("%s/%s: decoded protocol error lost the structured RPCError", record.Provider, record.Scenario)
		}
		if structured.DataPresence != expectedPresence(state) {
			return fmt.Errorf("%s/%s: RPCError.DataPresence = %s, want %s", record.Provider, record.Scenario, structured.DataPresence, expectedPresence(state))
		}
		states[state]++

		if record.Method != "eth_call" || response.Error.ErrorCode() != 3 {
			continue
		}
		plan, err := methods.PlanFor(methods.MethodCall)
		if err != nil {
			return fmt.Errorf("%s/%s: eth_call plan: %w", record.Provider, record.Scenario, err)
		}
		policy := plan.Resolver
		observations := []rpc.Observation{
			{Source: rpc.SourceRef{ID: "s1"}, Err: response.Error, RPCError: structured},
			{Source: rpc.SourceRef{ID: "s2"}, Err: response.Error, RPCError: structured},
		}
		decision, err := policy.Resolve(observations)
		if err != nil {
			return fmt.Errorf("%s/%s: eth_call resolve: %w", record.Provider, record.Scenario, err)
		}
		if decision.Status != rpc.Corroborated {
			return fmt.Errorf("%s/%s: eth_call status = %s, want corroborated", record.Provider, record.Scenario, decision.Status)
		}
		if len(decision.Support) != 2 {
			return fmt.Errorf("%s/%s: eth_call support = %d, want 2", record.Provider, record.Scenario, len(decision.Support))
		}
		dimension := findDimension(decision.Dimensions, "data")
		if dimension == nil {
			return fmt.Errorf("%s/%s: eth_call decision has no data dimension", record.Provider, record.Scenario)
		}
		wantReturned := state == "empty-hex" || state == "value"
		if dimension.Returned != wantReturned {
			return fmt.Errorf("%s/%s: eth_call data returned = %v, want %v", record.Provider, record.Scenario, dimension.Returned, wantReturned)
		}
		if wantReturned {
			present, value := dataField(decision.Value)
			if !present {
				return fmt.Errorf("%s/%s: eth_call Value is missing returned data", record.Provider, record.Scenario)
			}
			if strings.TrimSpace(value) != record.DataRaw {
				return fmt.Errorf("%s/%s: eth_call data = %s, want %s", record.Provider, record.Scenario, value, record.DataRaw)
			}
		}
		key, err := policy.Normalize(structured.Raw())
		if err != nil {
			return fmt.Errorf("%s/%s: eth_call normalize: %w", record.Provider, record.Scenario, err)
		}
		if keys[state] == nil {
			keys[state] = map[string]bool{}
		}
		keys[state][key] = true
		callChecks++
	}
	if err := checkNoStateCollapse(keys); err != nil {
		return err
	}
	printReplaySummary(out, index, states, chainChecks, callChecks)
	return nil
}

func loadCapture(dir string) (captureIndex, []captureRecord, error) {
	indexRaw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return captureIndex{}, nil, err
	}
	var index captureIndex
	if err := json.Unmarshal(indexRaw, &index); err != nil {
		return captureIndex{}, nil, fmt.Errorf("decode index: %w", err)
	}
	if index.Schema != captureSchema || len(index.Records) == 0 {
		return captureIndex{}, nil, fmt.Errorf("%s has no usable WSS evidence index", dir)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return captureIndex{}, nil, err
	}
	records := make([]captureRecord, 0, len(index.Records))
	for _, entry := range index.Records {
		if entry.File == "" || entry.SHA256 == "" {
			return captureIndex{}, nil, fmt.Errorf("index entry %s/%s is missing file or sha256", entry.Provider, entry.Scenario)
		}
		path := filepath.Join(root, filepath.FromSlash(entry.File))
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return captureIndex{}, nil, fmt.Errorf("record %s escapes the capture directory", entry.File)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return captureIndex{}, nil, err
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != entry.SHA256 {
			return captureIndex{}, nil, fmt.Errorf("%s sha256 = %s, index has %s", entry.File, got, entry.SHA256)
		}
		var record captureRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return captureIndex{}, nil, fmt.Errorf("decode %s: %w", entry.File, err)
		}
		if record.Schema != captureSchema {
			return captureIndex{}, nil, fmt.Errorf("%s schema = %s", entry.File, record.Schema)
		}
		if record.Provider != entry.Provider || record.Scenario != entry.Scenario || record.Method != entry.Method || record.RequestID != entry.RequestID || record.DataState != entry.DataState {
			return captureIndex{}, nil, fmt.Errorf("%s identity does not match the index", entry.File)
		}
		records = append(records, record)
	}
	return index, records, nil
}

func stateFromPresence(presence rpc.ErrorDataPresence) (string, error) {
	raw, present := presence.ErrorDataJSON()
	if !present {
		return "missing", nil
	}
	switch strings.TrimSpace(string(raw)) {
	case "null":
		return "null", nil
	case `"0x"`:
		return "empty-hex", nil
	default:
		return "value", nil
	}
}

func expectedPresence(state string) rpc.ErrorDataPresenceState {
	switch state {
	case "missing":
		return rpc.ErrorDataPresenceMissing
	case "null":
		return rpc.ErrorDataPresenceNull
	case "empty-hex", "value":
		return rpc.ErrorDataPresenceValue
	default:
		return rpc.ErrorDataPresenceUnknown
	}
}

func isProtocolState(state string) bool {
	for _, candidate := range protocolStates {
		if state == candidate {
			return true
		}
	}
	return false
}

func findDimension(dimensions []rpc.DimensionEvidence, name string) *rpc.DimensionEvidence {
	for i := range dimensions {
		if dimensions[i].Name == name {
			return &dimensions[i]
		}
	}
	return nil
}

func dataField(raw json.RawMessage) (bool, string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false, ""
	}
	data, ok := fields["data"]
	return ok, string(data)
}

func checkNoStateCollapse(keys map[string]map[string]bool) error {
	states := make([]string, 0, len(keys))
	for state := range keys {
		states = append(states, state)
	}
	sort.Strings(states)
	for i := 0; i < len(states); i++ {
		for j := i + 1; j < len(states); j++ {
			for key := range keys[states[i]] {
				if keys[states[j]][key] {
					return fmt.Errorf("eth_call states %s and %s collapse to comparison key %s", states[i], states[j], key)
				}
			}
		}
	}
	return nil
}

func printReplaySummary(out io.Writer, index captureIndex, states map[string]int, chainChecks, callChecks int) {
	fmt.Fprintf(out, "capture: %d providers, %d records, chain=%d, class=%s, level=%s\n",
		countProviders(index), len(index.Records), index.ChainID, index.ProviderClass, index.EvidenceLevel)
	fmt.Fprintf(out, "chain checks: %d\n", chainChecks)
	fmt.Fprintf(out, "call policy checks: %d\n", callChecks)
	fmt.Fprintf(out, "states: missing=%d null=%d empty-hex=%d value=%d\n",
		states["missing"], states["null"], states["empty-hex"], states["value"])
	for _, state := range protocolStates {
		if states[state] == 0 {
			fmt.Fprintf(out, "unobserved state: %s\n", state)
		}
	}
	if states["missing"] == 0 || states["null"] == 0 || states["empty-hex"] == 0 || states["value"] == 0 {
		fmt.Fprintln(out, "W4 exit not met: provider-backed WSS four-state goldens remain prohibited; production scope is governed separately by decision 0056.2")
		return
	}
	fmt.Fprintln(out, "all four states observed; provider-backed golden admission still requires maintainer review; no golden was written")
}

func countProviders(index captureIndex) int {
	seen := map[string]bool{}
	for _, entry := range index.Records {
		seen[entry.Provider] = true
	}
	return len(seen)
}
