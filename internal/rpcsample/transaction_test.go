package rpcsample

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransactionDiscoveryPreservesFailureAndFreezesTarget(t *testing.T) {
	hash := "0x" + strings.Repeat("ab", 32)
	block := "0x" + strings.Repeat("cd", 32)
	var calls []WireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request WireRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		calls = append(calls, request)
		result := `null`
		switch request.Method {
		case "eth_getBlockByNumber":
			result = fmt.Sprintf(`{"number":"0x1312d00","hash":%q,"transactions":[%q]}`, block, hash)
		case "eth_chainId":
			result = `"0x1"`
		case "net_version":
			result = `"1"`
		case "web3_clientVersion":
			result = `"test-client"`
		}
		if _, err := fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, request.ID, result); err != nil && r.Context().Err() == nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()
	plan := Plan{
		Schema:   PlanSchema,
		Network:  Network{ChainID: 1, Fork: "cancun"},
		Sources:  []Source{{ID: "failure", Endpoint: "http://invalid.test"}, {ID: "success", Endpoint: server.URL}},
		Requests: []Request{{ID: "discovery", Method: "eth_getBlockByNumber", Params: json.RawMessage(`["0x1312d00",false]`)}},
	}
	client := server.Client()
	client.Transport = transactionTestTransport{base: http.DefaultTransport}
	out := filepath.Join(t.TempDir(), "bundle")
	idx, err := (Collector{Client: client}).CollectTransaction(context.Background(), plan, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Records) != 16 || len(idx.Records[0].ErrorKinds) == 0 {
		t.Fatalf("lost fixed source failures: %+v", idx)
	}
	for _, request := range calls {
		switch request.Method {
		case "eth_getTransactionByHash", "eth_getTransactionReceipt":
			if string(request.Params) != fmt.Sprintf(`[%q]`, hash) {
				t.Fatalf("unfrozen hash: %s", request.Params)
			}
		case "eth_getTransactionByBlockHashAndIndex":
			if string(request.Params) != fmt.Sprintf(`[%q,"0x0"]`, block) {
				t.Fatalf("unfrozen index: %s", request.Params)
			}
		}
	}
	if _, err := (Collector{Client: client}).CollectTransaction(context.Background(), plan, out); err == nil {
		t.Fatal("existing output accepted")
	}
	body, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"compatibility": "unassessed"`) {
		t.Fatal("missing evidence-only status")
	}
	// Mirror the established strict replay metadata shape without importing
	// replay (which imports this package). Unknown sidecar fields must not leak.
	var batch struct {
		SchemaVersion     string  `json:"schema_version"`
		BatchID           string  `json:"batch_id"`
		Feature           string  `json:"feature"`
		CapturedAt        string  `json:"captured_at"`
		Network           Network `json:"network"`
		BlockHeight       *uint64 `json:"block_height"`
		Web3ClientVersion *string `json:"web3_client_version"`
		Note              string  `json:"note"`
	}
	file, err := os.Open(filepath.Join(out, "batch.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // Read-only fixture metadata; Decode owns data errors and Close commits no output.
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		t.Fatal(err)
	}
	if batch.SchemaVersion != "ethguard.batch/v1" || batch.BlockHeight == nil || *batch.BlockHeight != 20000000 {
		t.Fatalf("invalid batch metadata: %+v", batch)
	}
}

type transactionTestTransport struct {
	base http.RoundTripper
}

func (transport transactionTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "invalid.test" {
		return nil, fmt.Errorf("deliberate source failure")
	}
	return transport.base.RoundTrip(request)
}

func transactionResultRecord(source, method, result string) Record {
	return Record{
		Source:    sourceRecord{ID: source},
		RequestID: "test",
		Method:    method,
		Request:   `{"jsonrpc":"2.0","id":1}`,
		Response:  WireResponse{Body: `{"jsonrpc":"2.0","id":1,"error":null,"result":` + result + `}`},
	}
}

func TestTransactionDiscoveryRejectsUnboundAndWrongHeight(t *testing.T) {
	hash := "0x" + strings.Repeat("ab", 32)
	for _, body := range []string{
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"result":{"number":"0x1","hash":%q,"transactions":[%q]}}`, hash, hash),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":{"number":"0x2","hash":%q,"transactions":[%q]}}`, hash, hash),
	} {
		record := Record{Request: `{"jsonrpc":"2.0","id":1}`, Response: WireResponse{Body: body}}
		if _, err := discoverTransaction(record, "0x1"); err == nil {
			t.Fatalf("accepted invalid discovery: %s", body)
		}
	}
}

func TestTransactionTargetRequiresMatchingSourceIdentity(t *testing.T) {
	wrongHash := "0x" + strings.Repeat("ab", 32)
	rightHash := "0x" + strings.Repeat("cd", 32)
	block := "0x" + strings.Repeat("ef", 32)
	discovery := func(hash string) string {
		return fmt.Sprintf(`{"number":"0x1","hash":%q,"transactions":[%q]}`, block, hash)
	}
	records := []Record{
		transactionResultRecord("wrong", "eth_getBlockByNumber", discovery(wrongHash)),
		transactionResultRecord("right", "eth_getBlockByNumber", discovery(rightHash)),
		transactionResultRecord("wrong", "eth_chainId", `"0x2"`),
		transactionResultRecord("right", "eth_chainId", `"0x1"`),
	}
	target := chooseTransactionTarget(records, "0x1", 1)
	if target.Source != "right" || target.TransactionHash != rightHash {
		t.Fatalf("wrong-network discovery seeded target: %+v", target)
	}
	if findings := transactionIdentityFindings(records[2], json.RawMessage(`"0x2"`), Network{ChainID: 1}); len(findings) != 1 {
		t.Fatal("wrong-network observation not reported")
	}
	if findings := transactionIdentityFindings(transactionResultRecord("right", "net_version", `"99"`), json.RawMessage(`"99"`), Network{ChainID: 1}); len(findings) != 0 {
		t.Fatal("network ID incorrectly equated with chain ID")
	}
}

func TestTransactionReportSeparatesNestedExternalDifferences(t *testing.T) {
	records := []Record{
		transactionResultRecord("a", "eth_getTransactionByHash", `{"hash":"0x01","accessList":[{"address":"0x1234","storageKeys":[],"vendor":"a"}]}`),
		transactionResultRecord("b", "eth_getTransactionByBlockHashAndIndex", `{"hash":"0x02","accessList":[{"address":"0x1234","storageKeys":[],"vendor":"b"}]}`),
	}
	report := transactionReport(records, Network{ChainID: 1}, TransactionTarget{})
	if len(report.Observations) != 2 {
		t.Fatalf("missing observations: %+v", report)
	}
	shape := report.Observations[1]
	if shape.ShapeError == "" {
		t.Fatal("invalid official wire shape not reported by conformance")
	}
	if _, ok := shape.Official["/hash"]; !ok {
		t.Fatal("official hash misclassified")
	}
	if _, ok := shape.External["/accessList/0/vendor"]; !ok {
		t.Fatal("nested external field lost")
	}
	if strings.Join(shape.OfficialDifferences, ",") != "/hash" || strings.Join(shape.ExternalDifferences, ",") != "/accessList/0/vendor" {
		t.Fatalf("official/external differences mixed: %+v", shape)
	}
}
