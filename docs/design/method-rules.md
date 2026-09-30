由 TestMethodRulesMatchIndex 生成，勿手动编辑。

| 方法 | 锚定 | 区块引用形态 | pending 处理 | resolver | 比较键种类 | result |
| --- | --- | --- | --- | --- | --- | --- |
| eth_chainId | none | none | none | strict | quantity | canonical |
| eth_getBlockByHash | none | none | none | strict | method-specific | raw |
| eth_getBlockByNumber | none | none | local-observation | strict | method-specific | raw |
| eth_getBalance | hash@1 | eip1898 | local-observation | strict | quantity | canonical |
| eth_getTransactionReceipt | none | none | none | strict | method-specific | canonical |
| eth_getTransactionByHash | none | none | none | strict | method-specific | raw |
| eth_getBlockReceipts | hash@0 | none | local-reject | strict | method-specific | canonical |
| eth_getTransactionCount | hash@1 | eip1898 | local-observation | strict | quantity | canonical |
| eth_getStorageAt | hash@2 | eip1898 | local-observation | strict | method-specific | canonical |
| eth_getCode | hash@1 | eip1898 | local-observation | strict | method-specific | canonical |
| eth_call | hash@1 | eip1898 | local-observation | call-revert | method-specific | canonical |
| eth_estimateGas | hash@1 | eip1898 | local-observation | median | quantity | canonical |
| eth_getLogs | height@0.fromBlock,toBlock | none | local-reject | strict | method-specific | canonical |
| eth_feeHistory | height@1 | none | local-observation | strict | method-specific | canonical |
| eth_gasPrice | none | none | none | median | quantity | canonical |
| eth_maxPriorityFeePerGas | none | none | none | median | quantity | canonical |
| eth_blobBaseFee | none | none | none | median | quantity | canonical |
| eth_blockNumber | none | none | none | median | quantity | canonical |
| eth_createAccessList | hash@1 | eip1898 | local-observation | strict | method-specific | canonical |
| net_peerCount | none | none | none | local | quantity | canonical |
| eth_syncing | none | none | none | local | object | raw |
| net_version | none | none | none | strict | quantity | canonical |
| net_listening | none | none | none | local | object | raw |
