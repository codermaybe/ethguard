# EthGuard

[English](README.md) · **简体中文**

EthGuard 查询第三方 Ethereum execution RPC，按方法语义返回多源结果、明确状态和来源证据。已有固定成员裁决、移动状态共同参考、原生 HTTP／raw WSS、写入与订阅。

当前主体框架、锁定 go-ethereum v1.17.10 的47项目标方法名称集合和公开生成类型迁移已完成；逐方法语义、provider 与生命周期证据仍在完善。方法数和模型完成不代表完整兼容或稳定发布。进度见 [路线图](docs/design/project-roadmap.md)，具体缺口见 [覆盖清单](docs/design/method-transport-coverage.md)。

`Corroborated` 是 provider-backed cross-source evidence，不是 Ethereum 共识、规范链、密码学最终性或来源独立性证明。Selected、Forwarded 保留各自保证范围。

## 使用

按应用生命周期共享 Client，完成后关闭。连接惰性建立并复用，不按请求或 goroutine 新建 pool。

```go
client, err := ethguard.Dial("https://your-provider-a", "https://your-provider-b")
if err != nil { return err }
defer client.Close()

address := ethapi.Address("0x000000000000000000000000000000000000dead")
balance, result, err := client.BalanceAt(ctx, address, nil)
```

root 返回业务值、CallResult 和最终错误；CallResult 携带读取或广播证据、同步写入的有界收据观察及已保留材料，其 Err 与尾部错误一致。EthClient view 复用同一执行，仅省略 CallResult：

```go
view := client.EthClient()
balance, err := view.BalanceAt(ctx, address, nil)
```

公开业务模型统一为生成 ethapi：Address／Hash、Block／Header／Transaction／Receipt／Log、CallMsg、查询与 Simulate 模型。它们与 geth Go 名义类型不同，bind/v2 等接口的调用方需显式适配。写入接受提供 MarshalBinary 的 BinaryTransaction；geth transaction 可以提供字节，不重新成为公开业务模型。

单源需明确选择 Forwarded：

```go
client, err := ethguard.DialUncorroborated("https://your-provider")
```

Dial 拒绝单URL与重复endpoint身份；多个URL不证明独立运营者。构造不联网，DialContext只约束构造。链身份不隐式固定，可通过WithChainID或配置chain_id声明。

## SDK边界与诊断

公开SDK包括Client及EthClient view、生成ethapi模型、rpc数据合同、配置与观察值。pool、aggregator、方法计划、resolver、reference、transport和订阅状态机归各自internal owner；公开订阅类型直接alias唯一owner。原公开执行包和Client.Pool()已退出，不保留转发替身。

client.Sources()按构造顺序返回成员身份与健康值快照。client.Sample(ctx, method, params...)采样当前可用来源：参数冻结一次，遵循Client生命周期、收集超时、cooldown与latest-tip gate，记录transport健康；不建立reference、不做方法准入或裁决。返回的响应字节完整且归调用方独占，收集错误可以与已有观察同时返回。诊断比较另用Normalize。

## 配置

```go
client, err := ethguard.LoadFile("config.yaml")
```

```yaml
chain_id: 1
upstreams:
  - id: provider-a
    endpoint: https://rpc-a.example
  - id: provider-b
    endpoint: wss://rpc-b.example
    transport: wss
```

省略transport使用native HTTP，显式wss使用raw WebSocket adapter；显式来源transport与DialerOption不能混用。两种来源均参与固定成员读取与写入。更换成员需新建Client。

Go构造使用config.FromEndpoints和ethguard.New，可配置WithChainID、WithFinalizedAnchoring等同一配置模型的选项；Go链选项覆盖文件值。可运行示例为 `go run ./examples/basic -config ./config.yaml`。

## 结果、参考与材料

确定性typed读取要求对应门槛，支持不足返回分类错误；Median／Local保持Selected。通用只读Call按已注册合同执行，未知方法本地拒绝，不绕过准入或订阅建立。

默认锚点策略下，latest／safe／finalized按原level建立调用内reference；显式WithFinalizedAnchoring则让default/latest使用finalized选择，safe保留自身level。显式pin不换块；reference失败不发业务请求，故障不缩冻结分母。pending-local是来源本地观察，多源同值也不升级Corroborated。

完整block读取检查请求身份、适用header hash与body承诺（transaction／withdrawals root、uncle）；header-only不证明完整body。内容关系检查不证明canonical chain或finality。

按调用开启原始材料与可选完成捕获：

```go
base, err := ethguard.WithSourceEvidence(ctx, 64<<10)
if err != nil { return err }
callCtx, capture := ethguard.WithResult(base)
block, result, err := client.BlockByNumber(callCtx, nil)
_ = block
_ = result
saved, published, captureErr := capture.Result()
```

仅在published且captureErr为nil时读取saved。直接结果仍是业务返回；capture错误表达交付事实，句柄只用一次。保留受byte／record／text上限和截断规则约束，预算耗尽不改变请求、票数或门槛。私有算法类型不能表达某字段，不构成将受保护字段降为可选原文的理由，详见 [返回值合同](docs/design/return-value-design.md)。

## 写入、订阅与生命周期

写入校验冻结交易字节，并将来源报告的hash绑定到本地提交hash。WriteDecision分别记录接受状态、裁定原因及每个来源的通信、回答和方法判断。支持不足、合法异值、协议拒绝和foreign hash保持可区分；未确认结果不自动重放。

同步写入只广播一次，再轮询收据。CallResult.ReceiptWait保留最后尝试与最近形成的收据观察，各有自己的序号、Decision和错误。最后一次查询中断不会覆盖此前的null或冲突观察；操作Err只表达最终结论。这些事实不依赖可选原文预算，默认最多保留两个查询位置。库不签名、不选择gas、不分配nonce、不管理替换交易。

root和view的SubscribeNewHead(ctx, policy)返回提供Next、Basis、Close的HeadSubscription。事件包含完整连续路径变化、重组撤回、冻结路径证人与审计记录；固定WSS成员提供多数支持，缺失对象按hash恢复，内存、通知队列和请求并发显式限额。多源Client仅一个WSS来源会拒绝，显式单源Client保持Forwarded。

SubscribeFilterLogs(ctx,query,policy)返回提供Next/Basis/CommittedTip/Close的LogsSubscription，已于2026-11-24通过本地验收：沿Head出生路径逐块交付全部匹配日志、零命中及完整块撤回，内容通过交易与收据承诺证明；数字/hash过滤只筛选实时路径、不补历史。

SubscribeTransactionReceipts(ctx,query,policy)使用ReceiptsSubscriptionPolicy，返回具有相同交付操作的ReceiptsSubscription。nil/空查询表示Head出生块起全量实时收据；完整交易/收据承诺证明覆盖范围，固定Q比较实际交付的官方字段，包括未被收据根覆盖的声明。BlockSupplier与ReceiptSupport分别记录这两种证据，不引入链配置或冒称独立执行证明。收据连续流已于2026-11-25通过本地验收。策略字段、审计导出与保证边界见 [订阅合同](docs/design/subscription-contract.md)。

raw WSS提供ID demux与有界通知；adapter不自动重连／replay，root订阅worker可以重建来源会话。不承诺通用keepalive或无限恢复。HTTP/WSS错误presence、合成测试和真实provider观察分别记账。

任一facade Close停止共享Client，新请求在网络前失败，在途公开操作随owner结束。Client.Close触发订阅异步清理；需要完成屏障时，调用HeadSubscription.Close、LogsSubscription.Close或ReceiptsSubscription.Close，等待该流的任务和owner watcher全部退出。主动关闭或Next取消会放弃未读事件；生产端失败先排空已提交事件，再返回携带剩余审计的终态错误。终态可见时慢清理仍可继续，该流的Close仍等待其完成。

## 模块与验证

唯一生产装配位于internal/runtime；rpc只拥有公开请求、响应、裁定与证据数据合同及纯值函数。internal/rpcengine拥有Resolver、执行计划与裁决算法，internal/methods拥有方法语义，internal/projection装配官方字段投影，internal/aggregator编排调用，internal/pool拥有成员／收集／健康，internal/reference通过注入能力选择head。ethapi与internal/ethcodec提供模型和原语，internal/ethobject单树拥有绑定与对象保护，internal/typed只有一份生产投影。internal/transport与internal/subscription分别拥有协议和流，两种native adapter共用internal/rpcwire信封及错误保真。internal/uncertain拥有材料保留与预算，observe拥有被动事件及健康快照。

root读取按state_reads.go、block_reads.go、transaction_reads.go归组；contract_execution.go拥有call、gas、fee与access-list，simulate.go拥有模拟，read_helpers.go集中typed读与输入辅助。ethclient_view.go全部直接调用相同私有实现；block与header各自保留投影与错误合同。internal/diag集中livecanary、shapeprobe、rpcsample/replay、chainlist及活动fixture，核心生产包不得反向导入该树；cmd、internal/verification、internal/testkit与rpctest分别保留命令、公开验证和本地测试能力。

从 [文档导航](docs/design/README.md)进入完整现行合同。历史decision供追溯，不是另一份执行计划。公共管线、响应模型、read精调、同步写入及NewHead已验收；其余方法、运行期专题和外部证据按路线图独立推进。

默认测试离线；Anvil与provider采集显式运行。本地链／合成通过不关闭外部缺口；canary passed、workflow成功和发布批准分别表达。正式门禁、固定工具和产物流程仅在 [发布合同](docs/design/release-contract.md)维护。
