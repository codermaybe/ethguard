由 TestMethodRulesMatchIndex 生成，勿手动编辑。

| 方法 | 锚定 | 区块引用形态 | pending 处理 | resolver | 比较键种类 |
| --- | --- | --- | --- | --- | --- |
| eth_chainId | none | none | none | strict | quantity |
| eth_getBlockByHash | none | none | none | strict | method-specific |
| eth_getBlockByNumber | none | none | local-observation | strict | method-specific |
| eth_getBalance | hash@1 | eip1898 | local-observation | strict | quantity |
| eth_getTransactionReceipt | none | none | none | strict | method-specific |
| eth_getTransactionByHash | none | none | none | strict | method-specific |
| eth_getBlockReceipts | hash@0 | none | local-reject | strict | method-specific |
| eth_getTransactionCount | hash@1 | eip1898 | local-observation | strict | quantity |
| eth_getStorageAt | hash@2 | eip1898 | local-observation | strict | method-specific |
| eth_getCode | hash@1 | eip1898 | local-observation | strict | method-specific |
| eth_call | hash@1 | eip1898 | local-observation | call-revert | method-specific |
| eth_estimateGas | hash@1 | eip1898 | none | median | quantity |
| eth_getLogs | height@0.fromBlock,toBlock | none | local-reject | strict | method-specific |
| eth_feeHistory | height@1 | none | local-observation | strict | method-specific |
| eth_gasPrice | none | none | none | median | quantity |
| eth_maxPriorityFeePerGas | none | none | none | median | quantity |
| eth_blobBaseFee | none | none | none | median | quantity |
| eth_blockNumber | none | none | none | median | quantity |
| eth_createAccessList | hash@1 | eip1898 | local-observation | strict | method-specific |
| net_peerCount | none | none | none | local | quantity |
| eth_syncing | none | none | none | local | object |
| net_version | none | none | none | strict | quantity |
| net_listening | none | none | none | local | object |
