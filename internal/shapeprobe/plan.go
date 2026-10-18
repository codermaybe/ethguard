// Package shapeprobe collects one mainnet response shape per still-uncaptured
// read method. It records observations and compares them with the methods'
// existing normalizers. It does not adjudicate and it does not write a golden.
package shapeprobe

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/rpcsample"
	"github.com/codermaybe/ethguard/rpc/methods"
)

const (
	// MetaSchema is the probe batch metadata schema. It is not a golden batch.
	MetaSchema = "ethguard.shape-probe/v1"
	// ClientVersionMethod is context recorded beside the probe, not one of the
	// eleven methods and not a compatibility attribution.
	ClientVersionMethod    = "web3_clientVersion"
	ClientVersionRequestID = "web3_clientVersion"
)

// LogsWindow is the eth_getLogs block window pinned to one anchor height.
type LogsWindow struct {
	FromBlock string `json:"from_block"`
	ToBlock   string `json:"to_block"`
}

// HeightHex is the wire quantity used for a pinned height. The spelling is
// the unpadded 0x form, so the same height always produces the same text.
func HeightHex(height uint64) string {
	return fmt.Sprintf("0x%x", height)
}

// LogsWindowAt is a one-block window at the anchor. Both bounds are that
// height; a rerun at the same height rebuilds the same bounds.
func LogsWindowAt(height uint64) LogsWindow {
	hexHeight := HeightHex(height)
	return LogsWindow{FromBlock: hexHeight, ToBlock: hexHeight}
}

// ProbeRequests builds the collection plan for one anchor height. Block
// parameters appear only on eth_feeHistory and eth_getLogs, and only as this
// height. The other nine methods, plus the context-only clientVersion call,
// send an empty parameter list. Request order is fixed, so the JSON-RPC id
// assigned by the collector stays fixed too.
func ProbeRequests(height uint64) ([]rpcsample.Request, error) {
	window := LogsWindowAt(height)
	logsParams, err := json.Marshal([]any{map[string]string{
		"fromBlock": window.FromBlock,
		"toBlock":   window.ToBlock,
	}})
	if err != nil {
		return nil, err
	}
	feeParams, err := json.Marshal([]any{2, HeightHex(height), []float64{25, 50, 75}})
	if err != nil {
		return nil, err
	}
	empty := json.RawMessage("[]")
	ordered := []rpcsample.Request{
		{ID: methods.MethodChainID, Method: methods.MethodChainID, Params: empty},
		{ID: methods.MethodNetVersion, Method: methods.MethodNetVersion, Params: empty},
		{ID: methods.MethodGasPrice, Method: methods.MethodGasPrice, Params: empty},
		{ID: methods.MethodMaxPriorityFeePerGas, Method: methods.MethodMaxPriorityFeePerGas, Params: empty},
		{ID: methods.MethodBlobBaseFee, Method: methods.MethodBlobBaseFee, Params: empty},
		{ID: methods.MethodBlockNumber, Method: methods.MethodBlockNumber, Params: empty},
		{ID: methods.MethodSyncing, Method: methods.MethodSyncing, Params: empty},
		{ID: methods.MethodPeerCount, Method: methods.MethodPeerCount, Params: empty},
		{ID: methods.MethodListening, Method: methods.MethodListening, Params: empty},
		{ID: methods.MethodFeeHistory, Method: methods.MethodFeeHistory, Params: feeParams},
		{ID: methods.MethodGetLogs, Method: methods.MethodGetLogs, Params: logsParams},
		{ID: ClientVersionRequestID, Method: ClientVersionMethod, Params: empty},
	}
	return ordered, nil
}

// BlockParamRequest reports whether the request carries the anchor height.
func BlockParamRequest(method string) bool {
	return method == methods.MethodFeeHistory || method == methods.MethodGetLogs
}

// WireRequestJSON is the exact JSON-RPC request body the collector stores,
// before redaction. ordinal is the 1-based position in ProbeRequests.
func WireRequestJSON(ordinal int, req rpcsample.Request) (string, error) {
	body, err := json.Marshal(rpcsample.WireRequest{
		JSONRPC: "2.0",
		ID:      ordinal,
		Method:  req.Method,
		Params:  req.Params,
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// CheckPinnedRequests reports whether stored block-parameter requests still
// match the bodies ProbeRequests builds for height.
func CheckPinnedRequests(records []rpcsample.Record, height uint64) error {
	reqs, err := ProbeRequests(height)
	if err != nil {
		return err
	}
	want := map[string]string{}
	for i, req := range reqs {
		if !BlockParamRequest(req.Method) {
			continue
		}
		body, err := WireRequestJSON(i+1, req)
		if err != nil {
			return err
		}
		want[req.Method] = body
	}
	seen := map[string]bool{}
	for _, record := range records {
		expected, ok := want[record.Method]
		if !ok {
			continue
		}
		seen[record.Method] = true
		if record.Request != expected {
			return fmt.Errorf("%s/%s request = %s, want %s", record.Method, record.Source.ID, record.Request, expected)
		}
	}
	for method := range want {
		if !seen[method] {
			return fmt.Errorf("%s has no stored request", method)
		}
	}
	return nil
}
