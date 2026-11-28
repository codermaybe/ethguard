package ethadapter

import (
	"context"
	"log/slog"
	"time"
)

func (c *Client) observeSubscription(ctx context.Context, method string, started time.Time, detail *SubscriptionAudit, report ConversionReport, coreErr, adapterErr error) {
	if !c.observing() {
		return
	}
	defer func() { _ = recover() }()
	event := AuditEvent{Method: method, Duration: time.Since(started), Subscription: detail, ConversionReport: report, CoreErr: coreErr, AdapterErr: adapterErr}
	if c.audit != nil {
		c.audit(ctx, event)
		return
	}
	attrs := []slog.Attr{slog.String("method", method), slog.String("phase", detail.Phase), slog.Duration("duration", event.Duration), slog.Int("basis", len(detail.Basis.Members)), slog.Int("quorum", detail.Basis.Quorum), slog.Any("tier", detail.Basis.Tier), slog.Any("core_error", coreErr), slog.Any("adapter_error", adapterErr), slog.Any("omissions", report.Omissions)}
	var version uint64
	var appended, retracted int
	switch {
	case detail.Head != nil:
		version = detail.Head.Path.NewVersion
		appended = len(detail.Head.Path.Appended)
		retracted = len(detail.Head.Path.Retracted)
	case detail.Logs != nil:
		version = detail.Logs.Path.NewVersion
		appended = len(detail.Logs.Path.Appended)
		retracted = len(detail.Logs.Path.Retracted)
	case detail.Receipts != nil:
		version = detail.Receipts.Path.NewVersion
		appended = len(detail.Receipts.Path.Appended)
		retracted = len(detail.Receipts.Path.Retracted)
	}
	attrs = append(attrs, slog.Uint64("path_version", version), slog.Int("appended", appended), slog.Int("retracted", retracted))
	c.logger.LogAttrs(ctx, slog.LevelInfo, "ethadapter subscription", attrs...)
}
