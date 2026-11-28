package ethguard

import sub "github.com/codermaybe/ethguard/internal/subscription"

// ReceiptsSubscriptionPolicy independently bounds Head topology and receipt
// content. Zero durations disable deadlines; zero RetryDelay disables retries.
// The response bound reserves decode workspace before each supplier starts.
type ReceiptsSubscriptionPolicy = sub.ReceiptsSubscriptionPolicy
type ReceiptsProofKind = sub.ReceiptsProofKind

// ReceiptSupport describes the official values declared by distinct members
// in one attempt. It does not claim chain execution or fork-rule validation.
type ReceiptSupport = sub.ReceiptSupport
type ReceiptsDelivery = sub.ReceiptsDelivery
type ReceiptsPathDelta = sub.ReceiptsPathDelta
type ReceiptsEvent = sub.ReceiptsEvent
type ReceiptsTerminalError = sub.ReceiptsTerminalError

// ReceiptsSubscription follows its private Head's complete path from birth.
// It commits only complete content deltas; Next transfers caller ownership,
// and Close aborts unread delivery and joins all actual work.
type ReceiptsSubscription = sub.ReceiptsSubscription

const (
	ReceiptsTransactionsExcluded = sub.ReceiptsTransactionsExcluded
	ReceiptsEmptyBlock           = sub.ReceiptsEmptyBlock
	ReceiptsBlockCommitment      = sub.ReceiptsBlockCommitment
)
