package ethapi_test

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestPublicGeneratedValues(t *testing.T) {
	quantity, err := ethapi.NewGetBalanceResult(json.RawMessage(`"0x2a"`))
	if err != nil || quantity != ethapi.Quantity("0x2a") {
		t.Fatalf("public balance value = %q, %v", quantity, err)
	}
	if _, err := ethapi.NewGetBalanceResult(json.RawMessage(`null`)); err == nil {
		t.Fatal("non-null balance accepted null")
	}
	value, err := ethapi.NewSimulateCallResult(json.RawMessage(`{"status":"0x0","returnData":"0x","gasUsed":"0x1","error":{"code":3,"message":"execution reverted"}}`))
	if err != nil || value.ResultOfCallFailure == nil || value.ResultOfCallSuccess != nil {
		t.Fatalf("public failure branch = %+v, %v", value, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ethapi.NewSimulateCallResult(raw); err != nil {
		t.Fatalf("public model round trip: %v", err)
	}
}
