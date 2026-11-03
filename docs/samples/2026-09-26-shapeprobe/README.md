# 2026-09-26 mainnet 响应形状探查（shapeprobe 归档）

采集工具：`cmd/shapeprobe`（业务职责见 `docs/design/tooling.md`）。
锚定高度 0x18da226（26059302），4 个公开 mainnet 端点，读取方法含
eth_chainId、net_version、eth_blockNumber、eth_gasPrice、eth_blobBaseFee、
eth_feeHistory、eth_getLogs 等；`report.md` 是人工可读的观察报告，`records/`
保留逐端点原始响应（含 SHA-256），`batch.json`/`index.json` 是采集三件套的
机器可读部分。

本批次是真实 provider 证据归档，不参与任何测试断言，也不构成兼容性结论；
其「现有归一化未接受」分类是 2026-09-26 的观察快照。 reproduction 边界见
`report.md` 开头。同日无第二轮采集。
