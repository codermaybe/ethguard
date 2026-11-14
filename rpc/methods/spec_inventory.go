package methods

// Specs is the frozen MethodSpec inventory: one entry per Registered read
// method, each collected from its own method file. Compile turns it into the
// registry the composition root installs; the collection order is stable and
// follows client.go's construction order, so diagnostics remain
// deterministic.
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
