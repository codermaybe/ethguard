package ethguard

import sub "github.com/codermaybe/ethguard/internal/subscription"

type ContentReason = sub.ContentReason
type ContentRequestKind = sub.ContentRequestKind

// ContentAudit records one bounded completion; it retains no raw answer or
// provider error string. Request identity includes its block and attempt.
type ContentAudit = sub.ContentAudit

const (
	ContentRequestFailure    = sub.ContentRequestFailure
	ContentInvalid           = sub.ContentInvalid
	ContentRequestTimeout    = sub.ContentRequestTimeout
	ContentResponseTooLarge  = sub.ContentResponseTooLarge
	ContentStaleSession      = sub.ContentStaleSession
	ContentRequestCanceled   = sub.ContentRequestCanceled
	ContentRequestUnfinished = sub.ContentRequestUnfinished
	ContentVerified          = sub.ContentVerified
)

const (
	ContentLogProof     = sub.ContentLogProof
	ContentTransactions = sub.ContentTransactions
	ContentReceipts     = sub.ContentReceipts
)
