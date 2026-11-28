# EthGuard

[English](README.md) · **简体中文**

EthGuard 查询第三方 Ethereum execution RPC，按方法语义返回多源结果、明确状态和来源证据。已有固定成员裁决、移动状态共同参考、原生 HTTP／raw WSS／本地 IPC、写入与订阅。

统一构造、配置推导、WSS／IPC 共享传输、公开生成模型及锁定 go-ethereum v1.17.10 的 47 项方法适配已通过本地验收与全仓普通／race。Windows named pipe 真实运行仍待 CI。逐方法和 provider 证据保留各自范围，不代表完整兼容或稳定发布。进度见 [路线图](docs/design/project-roadmap.md)，具体缺口见 [覆盖清单](docs/design/method-transport-coverage.md)。

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

公开业务模型统一为生成 ethapi：Address／Hash、Block／Header／Transaction／Receipt／Log、CallMsg、查询与 Simulate 模型。它们与 geth Go 名义类型不同；需要 geth 业务类型时使用外围 ethadapter，其 47 项精确方法签名包含三类 channel 订阅，并满足 `bind/v2.ContractBackend` 和 `DeployBackend`。写入接受提供 MarshalBinary 的 BinaryTransaction；geth transaction 可以提供字节，不重新成为公开业务模型。


已有 go-ethereum 查询与交易代码可通过适配器使用同一套执行：

```go
client, err := ethadapter.Dial("https://rpc-a.example", "https://rpc-b.example")
if err != nil { return err }
defer client.Close()
balance, err := client.BalanceAt(ctx, common.HexToAddress("0x000000000000000000000000000000000000dead"), nil)
```

高级配置仍在 ethguard 完成，再以 `ethadapter.New(core)` 包装；`Core()` 返回同一个安全客户端。可选 WithAudit／WithLogger 观察转换与核心结果，外层 ethguard.WithResult 原样生效。非法值与内容承诺不符返回转换错误。具体转换和证据边界见[兼容合同](docs/design/go-ethereum-compatibility-policy.md)。

适配器订阅使用固定默认策略及熟悉的 geth channel 签名。原生路径出现撤回时，包括零匹配日志块，桥统一以 `ethguard.ErrReorgDetected` 终止；调用方处理错误并自行回滚或回扫。需要完整原子路径变化与证明时，使用 `Core().Subscribe*`。

一个端点自动进入 Forwarded，两个及以上不同端点进入 Majority：

```go
client, err := ethguard.Dial("https://your-provider")
```

空列表与重复端点身份直接拒绝；模式和成员在构造期固定，运行期掉源不会降级。多个 URL 不证明独立运营者。构造不联网，DialContext／OpenContext 仅在入口检查取消，不绑定客户端生命周期。链身份不隐式固定，可用 Open 传 WithChainID 或配置 chain_id。

```go
client, err := ethguard.Open([]string{"https://rpc-a.example", "https://rpc-b.example"}, ethguard.WithChainID(1))
```

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
  - endpoint: https://rpc-a.example
  - endpoint: wss://rpc-b.example
```

transport 从 endpoint 推导：HTTP(S) 使用原生 HTTP，WS(S) 使用 raw WebSocket，明确本地路径或 ipc:// 地址使用 IPC；显式填写时必须匹配。id 省略时自动分配 rpc-1、rpc-2…，跳过显式占用的名称。规范化后重复端点直接拒绝。各类来源均参与固定成员读取与写入；更换成员需新建 Client。

Open 直接接收端点列表与 Go 选项；config.FromEndpoints 和 ethguard.New 保留配置值入口。Go 链选项覆盖文件值。WithFinalizedAnchoring 要求至少两个不同来源，单源组合在构造期拒绝。可运行示例为 `go run ./examples/basic -config ./config.yaml`。

## 结果、参考与材料

多源模式下，确定性 typed 读取要求对应门槛，支持不足返回分类错误；Median／Local 保留各自选择合同。单源成功读取标为 Forwarded。通用只读 Call 按已注册合同执行，未知方法本地拒绝，不绕过准入或订阅建立。

默认锚点策略下，latest／safe／finalized按原level建立调用内reference；显式WithFinalizedAnchoring则让default/latest使用finalized选择，safe保留自身level。显式pin不换块；reference失败不发业务请求，故障不缩冻结分母。pending-local是来源本地观察，多源同值也不升级Corroborated。

单源 moving-tag 读取走同一个 ReferenceStage，以诚实的 Forwarded reference 固定业务请求。独立 AnchorBlockHash／AnchorBlockHashForTag／AnchorBlockHeight 仍要求 Corroborated。单源 TipTracking 可观察健康和报告高度，但不能证明相对同侪的新鲜度。

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

root和view的SubscribeNewHead(ctx, policy)返回提供Next、Basis、Close的HeadSubscription。事件包含完整连续路径变化、重组撤回、冻结路径证人与审计记录；构造期声明支持有界订阅的固定成员提供多数支持，缺失对象按hash恢复，内存、通知队列和请求并发显式限额。多源 Client 仅一个可订阅来源会拒绝，单源 Client 保持 Forwarded。WSS 与 IPC 均支持订阅。Unix IPC 已通过本地测试，Windows named pipe 运行验收仍待 CI。

SubscribeFilterLogs(ctx,query,policy)返回提供Next/Basis/CommittedTip/Close的LogsSubscription，已于2026-11-24通过本地验收：沿Head出生路径逐块交付全部匹配日志、零命中及完整块撤回，内容通过交易与收据承诺证明；数字/hash过滤只筛选实时路径、不补历史。

SubscribeTransactionReceipts(ctx,query,policy)使用ReceiptsSubscriptionPolicy，返回具有相同交付操作的ReceiptsSubscription。nil/空查询表示Head出生块起全量实时收据；完整交易/收据承诺证明覆盖范围，固定Q比较实际交付的官方字段，包括未被收据根覆盖的声明。BlockSupplier与ReceiptSupport分别记录这两种证据，不引入链配置或冒称独立执行证明。收据连续流已于2026-11-25通过本地验收。策略字段、审计导出与保证边界见 [订阅合同](docs/design/subscription-contract.md)。

原生调用方可直接使用 `ethguard.DefaultHeadSubscriptionPolicy()`、`DefaultLogsSubscriptionPolicy()` 或 `DefaultReceiptsSubscriptionPolicy()`，也可按需调整返回的结构体。手工构造的全零策略仍会被拒绝。默认值是有界准入额度，不是预分配内存；测量负载和限制见订阅合同。

WSS与IPC共用ID demux与有界通知；adapter不自动重连／replay，root订阅worker可以重建来源会话。不承诺通用keepalive或无限恢复。HTTP/WSS错误presence、合成测试和真实provider观察分别记账。

任一 facade Close 停止共享 Client，新请求在网络前失败，在途公开操作随 owner 结束。适配器订阅自动登记到共享客户端；Unsubscribe、adapter.Close 和 Core.Close 均解除发送阻塞，并等待原生清理、交付任务及 Err 关闭，即使调用方永久不读 channel。库不关闭调用方 channel，完成关闭屏障后不再发送。直接创建的原生流仍需各自调用 HeadSubscription.Close、LogsSubscription.Close 或 ReceiptsSubscription.Close，等待任务与 owner watcher 退出。原生主动关闭或 Next 取消放弃未读事件；生产失败先排空已提交事件，再交付带审计的终态错误。独立 Terminated／TerminalError 观察不等待排空，该流的 Close 仍等待清理完成。

## 模块与验证

唯一生产装配位于internal/runtime；rpc只拥有公开请求、响应、裁定与证据数据合同及纯值函数。internal/rpcengine拥有Resolver、执行计划与裁决算法，internal/methods拥有方法语义，internal/projection装配官方字段投影，internal/aggregator编排调用，internal/pool拥有成员／收集／健康，internal/reference通过注入能力选择head。ethapi与internal/ethcodec提供模型和原语，internal/ethobject单树拥有绑定与对象保护，internal/typed只有一份生产投影。internal/transport与internal/subscription分别拥有协议和流，原生传输共用internal/rpcwire信封及错误保真。internal/uncertain拥有材料保留与预算，observe拥有被动事件及健康快照。

外围 ethadapter 只通过公开核心合同提供 geth 类型、严格转换、可选审计与有界 channel 交付；共享客户端拥有执行与关闭登记，适配器不另建连接池或投票器。

root读取按state_reads.go、block_reads.go、transaction_reads.go归组；contract_execution.go拥有call、gas、fee与access-list，simulate.go拥有模拟，read_helpers.go集中typed读与输入辅助。ethclient_view.go全部直接调用相同私有实现；block与header各自保留投影与错误合同。internal/diag集中livecanary、shapeprobe、rpcsample/replay、chainlist及活动fixture，核心生产包不得反向导入该树；cmd、internal/verification、internal/testkit与rpctest分别保留命令、公开验证和本地测试能力。

从 [文档导航](docs/design/README.md)进入完整现行合同。历史decision供追溯，不是另一份执行计划。公共管线、响应模型、read精调、同步写入及NewHead已验收；其余方法、运行期专题和外部证据按路线图独立推进。

默认测试离线；Anvil与provider采集显式运行。本地链／合成通过不关闭外部缺口；canary passed、workflow成功和发布批准分别表达。正式门禁、固定工具和产物流程仅在 [发布合同](docs/design/release-contract.md)维护。
