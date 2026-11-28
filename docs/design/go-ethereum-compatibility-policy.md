# 官方业务语义与 go-ethereum 覆盖政策

状态：2026-11-28。原生 Client／EthClient 的 47 项业务方法名称集合已闭合。外围 `ethadapter` 的 47 项精确签名、三类 channel 订阅、`bind/v2.ContractBackend`／`DeployBackend` 消费及默认订阅预算已通过 S5／S6 本地验收与最终全仓普通／race。逐方法语义、真实 provider 与平台运行证据分别记账；PR #13 的 Windows adapter race 与真实 named pipe CI 已通过。

## 1. 三个调用表面，一个执行 owner

| 调用表面 | 业务类型与交付 | 适用场景 |
| --- | --- | --- |
| `ethguard.Client` | 生成 `ethapi` 模型、业务值与 `CallResult`；原生连续路径订阅 | 需要逐次裁决、证据或原子重组增量 |
| `ethguard.EthClient` | 同一 `ethapi` 模型，省略直接返回的 `CallResult`；保留原生订阅 | 使用标准模型的轻量调用方 |
| `ethadapter.Client` | 锁定 geth 类型与业务签名；调用同一个 `*ethguard.Client` | 复用 go-ethereum 查询、发送、合约调用代码 |

`ethadapter.New(core)` 与 `Core()` 共享生命周期，`Close()` 关闭同一个核心客户端。适配器没有独立连接、投票器或裸 `*gethrpc.Client` 访问器；只通过公开 root／ethapi／rpc 消费核心，核心不能反向依赖适配器。原 geth transport adapter 与公共 gethcompat 仍已退出，新适配器是业务类型消费者，不恢复旧执行路径。

原生业务模型仍以固定 execution-apis 原始来源及审核勘误生成；它们不具有 geth Go 名义类型身份。写入接受 `BinaryTransaction`，geth transaction 可提供其冻结签名字节。runtime 保持唯一生产装配点，未知公开 Call 本地拒绝。

## 2. 锁定版本与兼容范围

参考是 `go.mod` 锁定的 go-ethereum v1.17.10。`ethclient.Client` 有 48 个导出 receiver 方法，排除底层 `Client()` 访问器后目标为 47 项（含 `Close`）。包级构造函数与 `RevertErrorData` 不计入分母。

`scripts/ethclient-method-diff.sh` 检查原生 view 名称集合，`TestEthClientBusinessSignatures` 检查项目类型合同；适配器的独立反射测试逐项比较锁定 geth 的精确参数、返回值与 variadic 签名。S5 包含 44 项非订阅方法，S6 补齐三项订阅。方法集合与编译接口不证明参数接受域或逐方法行为完全相同。

S5 独立 26 项顶层合同测试及 adapter／methods／facade／root 四包 race 通过。测试覆盖精确签名、五类真实签名交易、历史 high-s 与大整数、全字段 Header、完整块内容根及 uncle、HTTP 实际请求、同步发送一次性与审计隔离。后续 PR #13 的 Windows job `140931942348` 已实际通过 adapter 整包 race 与真实 named pipe 测试。

验收同时修复核心 typed `FeeHistory` 的既有 wire 问题：blockCount 统一发送十六进制数量字符串，lastBlock 复用公共标签转换；root／view／adapter 三表面与合法标签、未知负值的 18 个子例通过。raw Call 原有数值输入接受域不变。

适配器自有的引用转换也接受锁定 geth 的 `EarliestBlockNumber(-5)`，与根入口一致映射为 `"0x0"`。该规则覆盖纯 FilterQuery 转换、FilterLogs／SubscribeFilterLogs、BlockReceipts 和 SimulateV1；未知负标签及超出 int64 的负值仍本地拒绝。最初本地验收漏测了这条转换路径，后续独立终审已补充公开入口反例并修复。

S5 验收覆盖 `bind/v2.DeployBackend` 与无订阅依赖的 `ethereum` 读写接口；S6 已补齐 `ContractBackend`、`ContractFilterer`、`LogFilterer`、`ChainReader`、`TransactionReader` 的编译断言及合约 watch 消费测试。`PendingStateEventer` 等不在本轮目标内。

对外表述为：常见查询、交易发送与合约调用沿用熟悉的 go-ethereum 接口；多源保证不足、本地拒绝域（如 pending block/logs）与订阅重组通过明确错误交付。构造统一按 0 源拒绝、1 源 Forwarded、2+ 不同源 Majority；掉源不缩固定成员、不自动降级。多 URL 不证明运营者独立，Corroborated 不等于链共识、canonicality 或 finality。

## 3. 适配转换与观察

转换固定采用两类规则，不提供 StrictLossless、OnReorg 等模式选项。

- 非法定长 hex、无符号负数、目标位宽溢出、未知交易类型、签名或内容承诺校验失败返回 `*ethadapter.ConversionError`。`*big.Int` 目标保留合法任意精度，历史签名接受域不额外套用未经提供的链激活规则。
- Header 包含锁定版本全部可选承诺字段（含 `BlockAccessListHash`、`SlotNumber`），缺失保持 nil，并复核 header hash。交易覆盖 Legacy／2930／1559／4844／7702，复核签名值与 transaction hash。
- 完整 Block 自动消费本次 `Decision.Provenance` 的同源 uncle 数组；逐项重验 uncle header 与 hash，并验证 uncle、transaction、withdrawals 根。纯 `ToGethBlock` 同样要求完整匹配的 uncle 证据，不能静默返回残缺块；不要求开启原始来源材料保留。
- geth 对象无法承载的 RPC 上下文、扩展或存在性区别通过 `ConversionReport` 交付。纯转换直接返回报告，Client 调用只有配置审计时才交付报告。未观察到 Forwarded 的扩展载体不等于证明没有扩展损失。

`WithAudit` 优先于 `WithLogger`，二者只观察，不改变执行或转换政策。审计事件区分 `CallResult`／核心错误与适配层转换错误；默认日志仅记录结果摘要。回调必须及时返回，不重入订阅关闭；观察 panic 不得把成功广播改成可重试失败。不开审计不构造 `AuditEvent` 或记录计时。

适配器直接调用原生方法，不重套 `WithResult`。外层 capture 始终记录核心真实结论：核心成功后转换失败，capture 仍是核心成功，方法返回转换错误。`WithSourceEvidence` 原样生效。完整两层交付合同见[返回值合同](return-value-design.md)。

核心 `RevertError` 保留 `Data`／`DataRaw`，不伪造 RPC error code 3。上游 `ethclient.RevertErrorData` 不能直接消费它；可用 `errors.As` 或 `ethadapter.RevertErrorData`。后者区分缺失／null／结构化数据与合法 hex，存在的 `"0x"` 返回非 nil 空字节及 true。

`SyncProgress` 原生模型仅有官方三个计数器；geth 扩展计数器无法表达为已观测值，适配报告明确提示。`FeeHistory` 的 blob 字段不在锁定 geth 结果中，也进入省略报告。`SimulateV1` 仍使用核心注册计划；geth 结果未承载的模拟块字段交付省略报告，类型转换验收不代替完整模拟语义或真实 provider 验收。

## 4. 订阅与实际证据

原生 Head／Logs／Receipts 都是 Next／Basis／Close 连续流；Logs／Receipts 还提供 CommittedTip。事件表达完整原子路径变化、撤回、空匹配块与证明。它们保留自己的策略和生命周期合同，不把 unary CallResult 冒充流事件。

适配器提供 `SubscribeNewHead`、`SubscribeFilterLogs` 与 `SubscribeTransactionReceipts` 的锁定 geth 签名，自动使用原生 `Default*SubscriptionPolicy()`。三类桥已通过独立合同测试及最终全仓普通／race，固定合同如下：

- 原生事件一旦包含 Retracted，终止桥并通过 Err 返回可由 `errors.Is(err, ethguard.ErrReorgDetected)` 识别的错误；零匹配日志块的撤回同样触发。该事实在转换额度预留及审计回调前确定，不以调用方是否已经从 channel 读取为条件。
- 正常零匹配日志不发送伪造 Log；Receipts 每个追加块发送一组完整的已选择收据，空组保留。PreBirthProof 和路径审计交给观察回调或 Core 的原生流，不伪造为 geth 对象。
- 桥固定使用两个待交接批次槽及 256 MiB 所有权额度。先以 NextWithBudget 取得原生事件租约，转换期间另计新对象与工作区；已转换批次单独保留至发送或丢弃。满队列或预算不足显式终止，原生终态观察独立于用户 channel 发送。
- Unsubscribe、adapter.Close 和 Core.Close 都解除发送阻塞，并等待原生 Close、交付任务退出及 Err 关闭；调用方提供的 channel 不由库关闭。建流 context 只约束建立阶段。

首个确定的终止原因胜出，随后观察到的撤回或错误可记录审计但不覆盖终态。原生 Terminated 已就绪时，读取失败路径采用其稳定原因（主动取消为 nil），不把取消引发的移交失败改判为桥故障；桥已确定的失败不会被后来原生关闭覆盖。携带审计的错误发布到调用方可见的缓冲 Err channel 后释放内部租约，不等待调用方实际接收；接口没有接收确认合同。停止与一个已经开始的 channel 发送竞争时，该次发送可能完成；Err 关闭、Unsubscribe 或客户端 Close 返回后不再发送。

订阅 `AuditEvent.Subscription` 以 setup／event／terminal 阶段携带固定 Basis 和对应原生事件，不能把空的 unary CallResult 解释成流裁决。setup 审计使用原始建流 context；event／terminal 只继承其值，去掉建流取消、Deadline 和 Cause，不把已经结束的建立期限带进长期观察。该 context 仅用于观察，不控制订阅生命周期。WithAudit 优先于 WithLogger；日志仅输出摘要。观察者需及时返回且不重入关闭，保留事件后由观察者承担该内存；回调 panic 和字段修改不改变已形成的重组／转换结论。

业务收到重组终态后仍需自行回滚或回扫；停止流不会撤销已落库数据。需要完整原子 Retracted＋Appended 的调用方使用 `Core().Subscribe*`。预算数值与原生所有权、终态及关闭边界归[订阅合同](subscription-contract.md)，实施门禁见[接入设计](access-plane-ipc-adapter.md#6-默认订阅策略与-ethadapter-channel-订阅桥合同)。

HTTP、WSS、IPC 的请求、订阅、写入及派生入口分别验收。Unix IPC 已有本地真实 socket／混合传输证据；Windows named pipe 已通过 CI 实机运行，逐项证据见[接入验收记录](access-plane-ipc-adapter.md#pr-13-复验通过与合入-master2026-11-28)。普通调用通过不推出全部订阅或客户端互操作兼容，合成测试不改标为真实 provider 证据。

## 5. 升级与发布

1. 升级固定 geth 参考时更新名称与精确签名差异、接口断言、字段转换和消费测试。
2. 官方字段变化沿[生成合同](wire-codegen.md)审计原始来源、勘误、模型与消费者；不手改生成物或增加平行 DTO。
3. 按入口核对请求、结果、错误、reference、证据、平台与生命周期；订阅另核对重组、背压、取消和资源回收。
4. 当前验证范围由[覆盖清单](method-transport-coverage.md)维护，发布对目标提交执行[发布门禁](release-contract.md)。构建、方法计数或一次 provider 观察均不能代替完整验收。
