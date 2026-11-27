package ethguard

import sub "github.com/codermaybe/ethguard/internal/subscription"

// HeadSubscriptionPolicy has no implicit resource or business-time defaults.
// MemoryBytes, RetainedNodes and MaxConcurrentRequests must be positive.
// Zero durations disable that deadline/TTL; RetryDelay zero disables retries.
// MaxRecoveryAttempts zero disables the attempt limit (the memory limit remains).
type HeadSubscriptionPolicy = sub.HeadSubscriptionPolicy
type HeadCommittedSample = sub.HeadCommittedSample

// Estimate runs outside all core locks and sees only committed, same-hash
// observations. Initial is retained until a sample from at least two sources.
// The callback must return promptly and must not call this subscription's Close.
type HeadGatePolicy = sub.HeadGatePolicy
type AnchorNode = sub.AnchorNode
type HeadEvidenceTier = sub.HeadEvidenceTier
type HeadPathWitness = sub.HeadPathWitness
type HeadObjectSupplier = sub.HeadObjectSupplier
type HeadEvaluation = sub.HeadEvaluation
type HeadAuditRange = sub.HeadAuditRange
type HeadAuditRecord = sub.HeadAuditRecord
type HeadAuditKind = sub.HeadAuditKind
type HeadAuditReason = sub.HeadAuditReason
type HeadAcquisition = sub.HeadAcquisition
type HeadEvidenceBasis = sub.HeadEvidenceBasis
type HeadTerminalError = sub.HeadTerminalError
type HeadPathChangeReason = sub.HeadPathChangeReason
type HeadDelivery = sub.HeadDelivery
type HeadPathDelta = sub.HeadPathDelta
type HeadEvent = sub.HeadEvent

// HeadSubscription owns all work for one stream. Next transfers event ownership;
// active Close discards queued events and joins every network operation.
type HeadSubscription = sub.HeadSubscription

const (
	HeadPush                 = sub.HeadPush
	HeadByHash               = sub.HeadByHash
	HeadTipChanged           = sub.HeadTipChanged
	HeadTipInvalidated       = sub.HeadTipInvalidated
	HeadSessionDisconnected  = sub.HeadSessionDisconnected
	HeadInvalidObject        = sub.HeadInvalidObject
	HeadRequestFailed        = sub.HeadRequestFailed
	HeadEvaluationReplaced   = sub.HeadEvaluationReplaced
	HeadWaitExpired          = sub.HeadWaitExpired
	HeadObserved             = sub.HeadObserved
	HeadReconnected          = sub.HeadReconnected
	HeadDisconnected         = sub.HeadDisconnected
	HeadTipExpired           = sub.HeadTipExpired
	HeadInvalidPush          = sub.HeadInvalidPush
	HeadNullObject           = sub.HeadNullObject
	HeadMalformedObject      = sub.HeadMalformedObject
	HeadWrongTarget          = sub.HeadWrongTarget
	HeadRequestTimeout       = sub.HeadRequestTimeout
	HeadRequestFailure       = sub.HeadRequestFailure
	HeadTargetChanged        = sub.HeadTargetChanged
	HeadGateExpired          = sub.HeadGateExpired
	HeadNotificationLimit    = sub.HeadNotificationLimit
	HeadMessageTooLarge      = sub.HeadMessageTooLarge
	HeadSourceSetupFailure   = sub.HeadSourceSetupFailure
	HeadCorroborated         = sub.HeadCorroborated
	HeadSingleSourceSelected = sub.HeadSingleSourceSelected
	HeadForwarded            = sub.HeadForwarded
	HeadInitialAnchor        = sub.HeadInitialAnchor
	HeadContinuousAdvance    = sub.HeadContinuousAdvance
	HeadReorg                = sub.HeadReorg
	HeadPreBirthReorg        = sub.HeadPreBirthReorg
)

var (
	ErrHeadMemoryBudget      = sub.ErrHeadMemoryBudget
	ErrHeadRecoveryExhausted = sub.ErrHeadRecoveryExhausted
	ErrHeadAuditTimeout      = sub.ErrHeadAuditTimeout
	ErrHeadClosed            = sub.ErrHeadClosed
	ErrHeadTerminalReturned  = sub.ErrHeadTerminalReturned
)
