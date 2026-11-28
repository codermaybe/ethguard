package ethguard

import sub "github.com/codermaybe/ethguard/internal/subscription"

// DefaultHeadSubscriptionPolicy returns the shared bounded Head preset.
// It does not change the rejection of manually constructed zero policies.
func DefaultHeadSubscriptionPolicy() HeadSubscriptionPolicy {
	return sub.DefaultHeadSubscriptionPolicy()
}

// DefaultLogsSubscriptionPolicy returns the shared bounded live-logs preset.
func DefaultLogsSubscriptionPolicy() LogsSubscriptionPolicy {
	return sub.DefaultLogsSubscriptionPolicy()
}

// DefaultReceiptsSubscriptionPolicy returns the shared bounded receipt preset.
func DefaultReceiptsSubscriptionPolicy() ReceiptsSubscriptionPolicy {
	return sub.DefaultReceiptsSubscriptionPolicy()
}
