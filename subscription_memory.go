package ethguard

import sub "github.com/codermaybe/ethguard/internal/subscription"

// SubscriptionMemoryBudget admits ownership transferred by NextWithBudget.
// Reserve must be nonblocking and must not synchronously Close the same stream.
// Its returned release must be idempotent. Ordinary callers can keep using Next;
// adapters use this boundary to account for events before native ownership ends.
type SubscriptionMemoryBudget = sub.MemoryBudget
