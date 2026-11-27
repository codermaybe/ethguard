package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	receiptobject "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
	"github.com/codermaybe/ethguard/rpc"
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
		KeySource:              rpcengine.RuleKeyCanonicalBytes,
		CompareKind:            rpcengine.RuleCompareMethodSpecific,
		ProcessCanonical:       processReceipt,
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

// processReceipt is the method's canonical processing entry: the single
// admission preparation validates the raw response against the request and
// hands out the very bytes it already checked, so the raw route's second
// Encode (the resolver's clustering encoder over the admitted response) is
// never repeated here. The raw route — Admit followed by the encoder —
// stays intact beside this entry for pending-local and raw-declared
// invocations. A failure returns the zero result with the
// unchanged admission error; a success carries no evidence, exactly what the
// raw admission returns for this method.
func processReceipt(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
	canonical, err := receiptobject.PrepareTransaction(req, value)
	if err != nil {
		return rpcengine.CanonicalResult{}, err
	}
	return rpcengine.NewCanonicalResult(canonical, nil)
}

func normalizeReceiptObject(value json.RawMessage) (string, error) {
	return receiptobject.Normalize(value, wiremask.PruneMethod, MethodGetTransactionReceipt)
}
