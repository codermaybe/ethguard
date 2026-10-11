package shapeprobe

import (
	"strings"
	"testing"

	"ethguard/rpc/methods"
)

func TestBlockParamRequestsRepeatAtSameAnchor(t *testing.T) {
	const height uint64 = 0x1234abcd
	first, err := ProbeRequests(height)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProbeRequests(height)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("len %d != %d", len(first), len(second))
	}
	var sawFee, sawLogs bool
	for i := range first {
		left, err := WireRequestJSON(i+1, first[i])
		if err != nil {
			t.Fatal(err)
		}
		right, err := WireRequestJSON(i+1, second[i])
		if err != nil {
			t.Fatal(err)
		}
		if left != right {
			t.Fatalf("%s request body changed\n%s\n%s", first[i].Method, left, right)
		}
		if !BlockParamRequest(first[i].Method) {
			if strings.Contains(left, "0x1234abcd") {
				t.Fatalf("%s should not carry the anchor: %s", first[i].Method, left)
			}
			continue
		}
		if !strings.Contains(left, "0x1234abcd") {
			t.Fatalf("%s lost the anchor: %s", first[i].Method, left)
		}
		switch first[i].Method {
		case methods.MethodFeeHistory:
			sawFee = true
		case methods.MethodGetLogs:
			sawLogs = true
			if !strings.Contains(left, `"fromBlock":"0x1234abcd"`) || !strings.Contains(left, `"toBlock":"0x1234abcd"`) {
				t.Fatalf("logs window = %s", left)
			}
		}
	}
	if !sawFee || !sawLogs {
		t.Fatal("block-parameter methods missing")
	}
	moved, err := ProbeRequests(height + 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if !BlockParamRequest(first[i].Method) {
			continue
		}
		left, err := WireRequestJSON(i+1, first[i])
		if err != nil {
			t.Fatal(err)
		}
		right, err := WireRequestJSON(i+1, moved[i])
		if err != nil {
			t.Fatal(err)
		}
		if left == right {
			t.Fatalf("%s ignored the height change", first[i].Method)
		}
	}
}
