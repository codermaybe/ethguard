package subscription

import "time"

// DefaultHeadSubscriptionPolicy returns a bounded general-purpose preset. It
// retains enough topology for a 20-block reorganization and several sources.
// Manual zero policies remain invalid. Limits bound ownership, not finality.
func DefaultHeadSubscriptionPolicy() HeadSubscriptionPolicy {
	return HeadSubscriptionPolicy{
		MemoryBytes: 32 << 20, RetainedNodes: 128, MaxConcurrentRequests: 4,
		WireMessages: 64, WireBytes: 1 << 20, MaxMessageBytes: 64 << 10,
		RequestTimeout: 10 * time.Second, RecoveryTimeout: 2 * time.Minute,
		AuditTimeout: 2 * time.Minute, RetryDelay: 250 * time.Millisecond,
	}
}

// DefaultLogsSubscriptionPolicy includes two concurrent 256 MiB decode
// reservations and retained content for a 20-block atomic path change. The
// logical allowance is not preallocated heap memory; exceeding it fails closed.
func DefaultLogsSubscriptionPolicy() LogsSubscriptionPolicy {
	return LogsSubscriptionPolicy{Head: DefaultHeadSubscriptionPolicy(), MemoryBytes: 1 << 30, MaxResponseBytes: 1 << 20, MaxConcurrentRequests: 2, RequestTimeout: 10 * time.Second, RecoveryTimeout: 2 * time.Minute, RetryDelay: 250 * time.Millisecond}
}

// DefaultReceiptsSubscriptionPolicy additionally covers retained raw candidates,
// transactions and 20 complete typed winners under the existing 64x accounting.
// Larger payloads or source sets may require an explicit native policy.
func DefaultReceiptsSubscriptionPolicy() ReceiptsSubscriptionPolicy {
	return ReceiptsSubscriptionPolicy{Head: DefaultHeadSubscriptionPolicy(), MemoryBytes: 2 << 30, MaxResponseBytes: 1 << 20, MaxConcurrentRequests: 2, RequestTimeout: 10 * time.Second, RecoveryTimeout: 2 * time.Minute, RetryDelay: 250 * time.Millisecond}
}
