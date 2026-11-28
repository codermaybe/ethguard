package ethguard_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestIndependentDefaultSubscriptionPoliciesAreCompleteFreshValues(t *testing.T) {
	head := ethguard.DefaultHeadSubscriptionPolicy()
	logs := ethguard.DefaultLogsSubscriptionPolicy()
	receipts := ethguard.DefaultReceiptsSubscriptionPolicy()
	if head.MemoryBytes == 0 || head.WireMessages <= 0 || head.MaxMessageBytes == 0 || head.WireBytes < head.MaxMessageBytes || head.MemoryBytes < head.WireBytes || head.RetainedNodes < 21 || head.MaxConcurrentRequests <= 0 || head.RequestTimeout <= 0 || head.RecoveryTimeout <= 0 {
		t.Fatalf("head preset cannot support bounded progress and a 20-block retained path: %+v", head)
	}
	for _, policy := range []struct {
		name             string
		memory, response uint64
		concurrency      int
	}{
		{"logs", logs.MemoryBytes, logs.MaxResponseBytes, logs.MaxConcurrentRequests},
		{"receipts", receipts.MemoryBytes, receipts.MaxResponseBytes, receipts.MaxConcurrentRequests},
	} {
		if policy.memory == 0 || policy.response == 0 || policy.concurrency <= 0 || policy.response > policy.memory {
			t.Fatalf("incomplete %s preset: %+v", policy.name, policy)
		}
	}
	if !reflect.DeepEqual(head, logs.Head) || !reflect.DeepEqual(head, receipts.Head) {
		t.Fatal("default content subscriptions silently use a different head policy")
	}
	head.MemoryBytes = 0
	logs.Head.MemoryBytes = 0
	receipts.MaxResponseBytes = 0
	if ethguard.DefaultHeadSubscriptionPolicy().MemoryBytes == 0 || ethguard.DefaultLogsSubscriptionPolicy().Head.MemoryBytes == 0 || ethguard.DefaultReceiptsSubscriptionPolicy().MaxResponseBytes == 0 {
		t.Fatal("caller mutation changed a later default policy")
	}
}

func TestIndependentExplicitZeroSubscriptionPolicyStillFailsLocally(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	client, err := ethguard.Dial(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	query, err := ethapi.NewFilterQuery([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeNewHead(t.Context(), ethguard.HeadSubscriptionPolicy{}); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("zero head policy: %v", err)
	}
	if _, err := client.SubscribeFilterLogs(t.Context(), query, ethguard.LogsSubscriptionPolicy{}); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("zero logs policy: %v", err)
	}
	if _, err := client.SubscribeTransactionReceipts(t.Context(), nil, ethguard.ReceiptsSubscriptionPolicy{}); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("zero receipts policy: %v", err)
	}
	if len(node.Calls()) != 0 {
		t.Fatal("invalid explicit policy contacted upstream")
	}
}
