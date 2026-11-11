package methods

import ()

// Specs is the frozen MethodSpec inventory: one entry per Registered read
// method, each declaring resolver, value domain, reference need, pending
// handling, hooks, and projector. Compile turns it into the registry the
// composition root installs; the hand-written constructors remain the
// public surface the equivalence tests hold together with these specs.
func Specs() []MethodSpec {
	return []MethodSpec{
		chainIDSpec(),
		getBlockByHashSpec(),
		getBlockByNumberSpec(),
		getBalanceSpec(),
		getTransactionReceiptSpec(),
		transactionByHashSpec(),
		transactionInBlockSpec(),
		getBlockReceiptsSpec(),
		getTransactionCountSpec(),
		blockTransactionCountByHashSpec(),
		blockTransactionCountByNumberSpec(),
		getStorageAtSpec(),
		getCodeSpec(),
		callSpec(),
		estimateGasSpec(),
		getLogsSpec(),
		feeHistorySpec(),
		gasPriceSpec(),
		maxPriorityFeePerGasSpec(),
		blobBaseFeeSpec(),
		blockNumberSpec(),
		createAccessListSpec(),
		peerCountSpec(),
		syncingSpec(),
		netVersionSpec(),
		listeningSpec(),
	}
}
