package projection

import "testing"

func TestExecutionAPIStandardFieldsDerivesPinnedPaths(t *testing.T) {
	fee, err := ExecutionAPIStandardFields("eth_feeHistory")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/oldestBlock", "/baseFeePerGas/*", "/baseFeePerBlobGas/*", "/blobGasUsedRatio/*"} {
		if !containsStandardPath(fee, path) {
			t.Fatalf("fee history path %q missing from %#v", path, fee)
		}
	}
	chainID, err := ExecutionAPIStandardFields("eth_chainId")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStandardPath(chainID, "") {
		t.Fatalf("scalar root missing: %#v", chainID)
	}
	logs, err := ExecutionAPIStandardFields("eth_getLogs")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStandardPath(logs, "/*/address") {
		t.Fatalf("log wildcard path missing: %#v", logs)
	}
	if containsStandardPath(logs, "") {
		t.Fatalf("composite array schema introduced a false root field: %#v", logs)
	}
	receipt, err := ExecutionAPIStandardFields("eth_getTransactionReceipt")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStandardPath(receipt, "/logs") {
		t.Fatalf("receipt container path missing: %#v", receipt)
	}
}

func TestProductionProjectorsCoverPinnedOfficialPaths(t *testing.T) {
	gaps, err := ProductionStandardFieldCoverageGaps()
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("production projector coverage gaps: %#v", gaps)
	}
}

func containsStandardPath(fields []StandardField, path string) bool {
	for _, field := range fields {
		if field.Path == path {
			return true
		}
	}
	return false
}
