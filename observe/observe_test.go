package observe

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

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

func TestZeroLogFallsBackToDefault(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero Log panicked: %v", r)
		}
	}()
	Log{}.RecordDecision(DecisionEvent{Method: "m", Status: "verified"})
	if !strings.Contains(buf.String(), "ethguard decision") {
		t.Fatalf("expected fallback record in %q", buf.String())
	}
}

func TestNopIsSafeDefault(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nop recorder panicked: %v", r)
		}
	}()
	Nop.RecordDecision(DecisionEvent{Status: "verified"})
	Nop.RecordDecision(DecisionEvent{Method: "m", Status: "failed", Conflict: true})
}
