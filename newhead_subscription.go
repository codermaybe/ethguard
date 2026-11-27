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
type HeadEvidenceTier = sub.EvidenceTier
type HeadPathWitness = sub.PathWitness
type HeadObjectSupplier = sub.ObjectSupplier
type HeadEvaluation = sub.EvaluationSnapshot
type HeadAuditRange = sub.AuditRange
type HeadAuditRecord = sub.AuditRecord
type HeadAuditKind = sub.AuditKind
type HeadAuditReason = sub.AuditReason
type HeadAcquisition = sub.Acquisition
type HeadEvidenceBasis = sub.EvidenceBasis
type HeadTerminalError = sub.TerminalError
type HeadPathChangeReason = sub.PathChangeReason
type HeadDelivery = sub.HeadDelivery
type HeadPathDelta = sub.HeadPathDelta
type HeadEvent = sub.HeadEvent

// HeadSubscription owns all work for one stream. Next transfers event ownership;
// active Close discards queued events and joins every network operation.
type HeadSubscription = sub.HeadSubscription

const (
	HeadPush                 = sub.Push
	HeadByHash               = sub.ByHash
	HeadTipChanged           = sub.TipChanged
	HeadTipInvalidated       = sub.TipInvalidated
	HeadSessionDisconnected  = sub.SessionDisconnected
	HeadInvalidObject        = sub.InvalidObject
	HeadRequestFailed        = sub.RequestFailed
	HeadEvaluationReplaced   = sub.EvaluationReplaced
	HeadWaitExpired          = sub.WaitExpired
	HeadObserved             = sub.ReasonObservation
	HeadReconnected          = sub.ReasonReconnect
	HeadDisconnected         = sub.ReasonDisconnected
	HeadTipExpired           = sub.ReasonTipExpired
	HeadInvalidPush          = sub.ReasonInvalidPush
	HeadNullObject           = sub.ReasonNullObject
	HeadMalformedObject      = sub.ReasonInvalidObject
	HeadWrongTarget          = sub.ReasonWrongTarget
	HeadRequestTimeout       = sub.ReasonRequestTimeout
	HeadRequestFailure       = sub.ReasonRequestFailure
	HeadTargetChanged        = sub.ReasonTargetChanged
	HeadGateExpired          = sub.ReasonGateExpired
	HeadNotificationLimit    = sub.ReasonNotificationLimit
	HeadMessageTooLarge      = sub.ReasonMessageTooLarge
	HeadSourceSetupFailure   = sub.ReasonSourceSetupFailure
	HeadCorroborated         = sub.Corroborated
	HeadSingleSourceSelected = sub.SingleSourceSelected
	HeadForwarded            = sub.Forwarded
	HeadInitialAnchor        = sub.InitialAnchor
	HeadContinuousAdvance    = sub.ContinuousAdvance
	HeadReorg                = sub.Reorg
	HeadPreBirthReorg        = sub.PreBirthReorg
)

var (
	ErrHeadMemoryBudget      = sub.ErrMemoryBudget
	ErrHeadRecoveryExhausted = sub.ErrRecoveryExhausted
	ErrHeadAuditTimeout      = sub.ErrAuditTimeout
	ErrHeadClosed            = sub.ErrClosed
	ErrHeadTerminalReturned  = sub.ErrNextAfterTerminal
)
