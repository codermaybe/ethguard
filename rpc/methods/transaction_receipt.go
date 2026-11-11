package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	receiptobject "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetTransactionReceipt is the JSON-RPC method spelling this file
// owns: the single receipt read.
const MethodGetTransactionReceipt = "eth_getTransactionReceipt"

// getTransactionReceiptSpec is the method's compiled contract: canonical
// receipt-object comparison over the strict resolver with the receipt
// admission binding. No whole-key compatibility correction applies to this
// method.
func getTransactionReceiptSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetTransactionReceipt,
		Resolver:               ResolverStrict,
		Normalize:              normalizeReceiptObject,
		Encode:                 receiptobject.Encode,
		KeySource:              rpc.RuleKeyCanonicalBytes,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Admit:                  admitReceipt,
		ObservationProjectorID: ProjectorReceipt,
	}
}

func getTransactionReceiptDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetTransactionReceipt,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "TransactionReceipt",
		Alignment: "v1.17.10",
	}
}

func admitReceipt(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receiptobject.AdmitTransaction(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeReceiptObject(value json.RawMessage) (string, error) {
	key, err := receiptobject.Normalize(value, wire.PruneMethod, MethodGetTransactionReceipt)
	if err != nil || key == "null" {
		return key, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &obj); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(obj)
	return string(encoded), err
}
