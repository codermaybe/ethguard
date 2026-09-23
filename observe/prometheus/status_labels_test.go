package prometheus

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"ethguard/aggregator"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpctest"
)

func TestDocumentedDecisionStatusLabelsMatchEmissions(t *testing.T) {
	documented := documentedStatusLabels(t)
	emitted := map[string]struct{}{}
	for _, status := range emitDecisionStatuses(t) {
		emitted[status] = struct{}{}
	}
	if len(emitted) != len(documented) {
		t.Fatalf("emitted %v, documented %v", keys(emitted), documented)
	}
	for _, status := range documented {
		if _, ok := emitted[status]; !ok {
			t.Fatalf("documented status %q was not emitted; emitted %v", status, keys(emitted))
		}
	}
	raw := metricsSource(t)
	for _, want := range []string{
		"Dashboard and alert migration",
		"ethguard_conflicts_total",
		`status="failed"`,
		"Do not alert on the absence of",
		"status=\"corroborated\"",
		"Decision.Status Unknown",
		"observe.DecisionEvent.Method",
		"observe.SourceEvent.Source",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("migration comment missing %q", want)
		}
	}
}

func documentedStatusLabels(t *testing.T) []string {
	t.Helper()
	const marker = "Documented emitted status labels:"
	raw := metricsSource(t)
	line := ""
	for _, candidate := range strings.Split(raw, "\n") {
		if strings.Contains(candidate, marker) {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatalf("metrics comment lost %q", marker)
	}
	_, list, ok := strings.Cut(line, marker)
	if !ok {
		t.Fatal("status marker has no list")
	}
	var labels []string
	for _, part := range strings.Split(list, ",") {
		label := strings.Trim(strings.TrimSpace(part), ".")
		if label == "" {
			continue
		}
		labels = append(labels, label)
	}
	return labels
}

func metricsSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func emitDecisionStatuses(t *testing.T) []string {
	t.Helper()
	var got []string
	got = append(got, emitStatus(t, 2, false, false, func(i int, n *rpctest.Node) {
		n.Return("m", "0x1")
	}))
	got = append(got, emitStatus(t, 1, false, false, func(i int, n *rpctest.Node) {
		n.Return("m", "0x1")
	}))
	got = append(got, emitStatus(t, 1, true, false, func(i int, n *rpctest.Node) {
		n.Return("m", "0x1")
	}))
	got = append(got, emitStatus(t, 2, false, true, func(i int, n *rpctest.Node) {
		if i == 0 {
			n.Return("m", "0x1")
		} else {
			n.Return("m", "0x2")
		}
	}))
	return got
}

func emitStatus(t *testing.T, count int, forwarded, wantErr bool, script func(int, *rpctest.Node)) string {
	t.Helper()
	metrics := New()
	p := pool.NewPool()
	t.Cleanup(p.Close)
	for i := range count {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		script(i, node)
		if _, err := p.Join(string(rune('a'+i)), node.URL()); err != nil {
			t.Fatal(err)
		}
	}
	opts := []aggregator.Option{aggregator.WithRecorder(metrics), aggregator.WithCollectTimeout(5 * time.Second)}
	if forwarded {
		shared, err := rpc.NewCorroborationShared(rpc.CorroborationForwarded, 0)
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, aggregator.WithCorroboration(shared))
	}
	a := aggregator.New(p, rpc.Registry{"m": quantityPolicy()}, opts...)
	if _, err := a.Aggregate(context.Background(), rpc.Request{Method: "m"}); (err != nil) != wantErr {
		t.Fatalf("aggregate error = %v, wantErr %v", err, wantErr)
	}
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var status string
	for _, family := range families {
		if family.GetName() != "ethguard_decisions_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "status" {
					if status != "" {
						t.Fatalf("one decision emitted multiple status labels: %s and %s", status, label.GetValue())
					}
					status = label.GetValue()
				}
			}
		}
	}
	if status == "" {
		t.Fatal("decision emitted no status label")
	}
	return status
}

func quantityPolicy() rpc.Policy {
	return rpc.Strict(func(v json.RawMessage) (string, error) {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", err
		}
		return s, nil
	})
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out
}
