package ethguard

import sub "github.com/codermaybe/ethguard/internal/subscription"

// LogsSubscriptionPolicy separates Head topology memory from log content
// memory. Both limits and content concurrency/response limits must be positive.
// MaxResponseBytes is an application admission limit; transport still owns its
// independent wire-body limit. Each active content supplier reserves a bounded
// decode workspace before its calls. Zero durations disable their deadlines;
// RetryDelay zero disables retries, MaxRecoveryAttempts zero disables its cap.
// A single request slot with no timeout can wait indefinitely for that source.
type LogsSubscriptionPolicy = sub.LogsSubscriptionPolicy
type LogsProofKind = sub.LogsProofKind
type LogsDelivery = sub.LogsDelivery
type LogsPathDelta = sub.LogsPathDelta
type LogsEvent = sub.LogsEvent
type LogsTerminalError = sub.LogsTerminalError
type LogsSubscription = sub.LogsSubscription

const (
	LogsFilterExcluded  = sub.LogsFilterExcluded
	LogsBloomExcluded   = sub.LogsBloomExcluded
	LogsBlockCommitment = sub.LogsBlockCommitment
)
