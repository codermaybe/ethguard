package observe

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"ethguard/pool"
	"ethguard/rpc"
)

func testSources(t *testing.T, names ...string) []*pool.Source {
	t.Helper()
	p := pool.NewPool()
	out := make([]*pool.Source, 0, len(names))
	for i, name := range names {
		src, err := p.Join(name, fmt.Sprintf("http://127.0.0.1:%d", 9000+i))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, src)
	}
	return out
}

func TestBuildDecisionReportsVerifiedWithAttempts(t *testing.T) {
	sources := testSources(t, "alpha", "beta", "gamma")
	emissions := []pool.Emission{
		{Src: sources[0], Val: []byte(`"0x1"`), RTT: 10 * time.Millisecond},
		{Src: sources[1], Val: []byte(`"0x1"`), RTT: 20 * time.Millisecond},
		{Src: sources[2], Err: fmt.Errorf("timeout"), RTT: 30 * time.Millisecond},
		{Src: sources[0]}, // never attempted: must be skipped
	}
	event := BuildDecision(time.Now().Add(-50*time.Millisecond), "eth_chainId",
		rpc.Decision{Status: rpc.Verified}, nil, emissions)

	if event.Method != "eth_chainId" || event.Status != "verified" || event.Conflict {
		t.Fatalf("unexpected event header: %+v", event)
	}
	if event.Faults != 0 {
		t.Fatalf("expected zero faults, got %d", event.Faults)
	}
	if len(event.Attempts) != 3 {
		t.Fatalf("expected three attempted sources, got %+v", event.Attempts)
	}
	if event.Attempts[1].Source != "beta" || event.Attempts[1].RTT != 20*time.Millisecond {
		t.Fatalf("unexpected attempt record: %+v", event.Attempts[1])
	}
	if !event.Attempts[2].Failed {
		t.Fatal("expected gamma attempt marked failed")
	}
	if event.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", event.Duration)
	}
}

func TestBuildDecisionMarksConflictAsFailed(t *testing.T) {
	sources := testSources(t, "alpha")
	emissions := []pool.Emission{{Src: sources[0], Val: []byte(`"0x2"`), RTT: time.Millisecond}}
	err := fmt.Errorf("strict: %w", rpc.ErrConflict)
	event := BuildDecision(time.Now(), "eth_chainId", rpc.Decision{Status: rpc.Verified}, err, emissions)
	if event.Status != "failed" || !event.Conflict {
		t.Fatalf("expected failed conflict event, got %+v", event)
	}
}

func TestLogLevelsMatchOutcome(t *testing.T) {
	cases := []struct {
		name  string
		event DecisionEvent
		want  string
	}{
		{"clean", DecisionEvent{Method: "m", Status: "verified"}, "level=DEBUG"},
		{"faulted", DecisionEvent{Method: "m", Status: "verified", Faults: 1}, "level=INFO"},
		{"conflict", DecisionEvent{Method: "m", Status: "failed", Conflict: true}, "level=WARN"},
		{"failed", DecisionEvent{Method: "m", Status: "failed"}, "level=WARN"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		rec := NewLog(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		rec.RecordDecision(tc.event)
		if !strings.Contains(buf.String(), tc.want) {
			t.Fatalf("%s: expected %s in %q", tc.name, tc.want, buf.String())
		}
	}
}

func TestLogFoldsAttemptsIntoOneEvent(t *testing.T) {
	var buf bytes.Buffer
	rec := NewLog(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	rec.RecordDecision(DecisionEvent{
		Method:   "eth_chainId",
		Status:   "failed",
		Conflict: true,
		Faults:   1,
		Duration: 5 * time.Millisecond,
		Attempts: []SourceEvent{
			{Source: "alpha", RTT: 3 * time.Millisecond},
			{Source: "beta", RTT: 4 * time.Millisecond, Failed: true},
		},
	})
	out := buf.String()
	for _, want := range []string{
		"msg=\"ethguard decision\"",
		"method=eth_chainId",
		"conflict=true",
		"faults=1",
		"sources=2",
		"source=alpha",
		"source=beta",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in %q", want, out)
		}
	}
	if got := strings.Count(out, "ethguard decision"); got != 1 {
		t.Fatalf("expected exactly one log record per decision, got %d", got)
	}
}

func TestNopIsSafeDefault(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nop recorder panicked: %v", r)
		}
	}()
	Nop.RecordDecision(DecisionEvent{Status: "verified"})
	Nop.RecordDecision(DecisionEvent{Method: "m", Status: "failed", Conflict: errors.Is(rpc.ErrConflict, rpc.ErrConflict)})
}
