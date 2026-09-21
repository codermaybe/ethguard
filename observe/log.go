package observe

import (
	"context"
	"log/slog"
)

// Log adapts a structured logger to the Recorder seam. It emits exactly one
// record per aggregate decision with per-source attempts folded in as a
// group, so log volume scales with decisions rather than fleet size.
//
// Levels: clean successes log at Debug, successes carrying faults at Info,
// conflicts and failed calls at Warn.
type Log struct {
	Logger *slog.Logger
}

// NewLog returns a Recorder writing through l. A nil logger falls back to
// slog.Default().
func NewLog(l *slog.Logger) Log {
	if l == nil {
		l = slog.Default()
	}
	return Log{Logger: l}
}

// RecordDecision emits the single per-decision structured event. A zero Log
// falls back to slog.Default so a miswired recorder degrades to stderr
// instead of panicking the call path.
func (l Log) RecordDecision(e DecisionEvent) {
	logger := l.Logger
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelDebug
	switch {
	case e.Conflict || e.Status == "failed":
		level = slog.LevelWarn
	case e.Faults > 0:
		level = slog.LevelInfo
	}

	attrs := make([]slog.Attr, 0, 7)
	attrs = append(attrs,
		slog.String("method", e.Method),
		slog.String("status", e.Status),
		slog.Bool("conflict", e.Conflict),
		slog.Int("faults", e.Faults),
		slog.Int("sources", len(e.Attempts)),
		slog.Duration("duration", e.Duration),
	)
	if len(e.Attempts) > 0 {
		group := make([]any, len(e.Attempts))
		for i, attempt := range e.Attempts {
			group[i] = slog.Group("attempt",
				slog.String("source", attempt.Source),
				slog.Duration("rtt", attempt.RTT),
				slog.Bool("failed", attempt.Failed),
			)
		}
		attrs = append(attrs, slog.Any("attempts", group))
	}
	logger.LogAttrs(context.Background(), level, "ethguard decision", attrs...)
}
