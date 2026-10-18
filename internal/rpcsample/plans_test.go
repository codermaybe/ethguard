package rpcsample

// Offline validation of the prepared capture plans (capture-tasks.md T0.6).
// Loading validates schema and structure only; no plan execution happens and
// no test touches the network.

import (
	"testing"
)

func TestPreparedPlansLoadAndValidate(t *testing.T) {
	plans := map[string][]string{
		"testdata/plans/read-local.plan.json": {
			"eth_chainId", "net_version", "eth_blockNumber", "net_peerCount", "net_listening",
		},
		"testdata/plans/read-volatile.plan.json": {
			"eth_gasPrice", "eth_maxPriorityFeePerGas", "eth_blobBaseFee", "eth_syncing", "eth_syncing",
		},
	}
	for path, wantMethods := range plans {
		t.Run(path, func(t *testing.T) {
			plan, err := LoadPlan(path)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Network.ChainID != 1 {
				t.Fatalf("network = %+v, want chain ID 1", plan.Network)
			}
			if len(plan.Sources) == 0 {
				t.Fatal("no sources")
			}
			for _, source := range plan.Sources {
				if source.Endpoint != "" || source.EndpointEnv == "" {
					t.Fatalf("source %q must use endpoint_env only", source.ID)
				}
			}
			if len(plan.Requests) != len(wantMethods) {
				t.Fatalf("requests = %d, want %d", len(plan.Requests), len(wantMethods))
			}
			for i, request := range plan.Requests {
				if request.Method != wantMethods[i] {
					t.Fatalf("request %d = %s, want %s", i, request.Method, wantMethods[i])
				}
				if len(request.Params) == 0 || string(request.Params) == "null" {
					t.Fatalf("request %s must keep explicit empty params", request.ID)
				}
			}
		})
	}
}

func TestPreparedVolatilePlanCoversBothSyncingShapes(t *testing.T) {
	plan, err := LoadPlan("testdata/plans/read-volatile.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var syncingIDs []string
	for _, request := range plan.Requests {
		if request.Method == "eth_syncing" {
			syncingIDs = append(syncingIDs, request.ID)
		}
	}
	if len(syncingIDs) < 2 {
		t.Fatalf("eth_syncing request ids = %v, want distinct ids for the two observed shapes", syncingIDs)
	}
}
