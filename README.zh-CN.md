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

root 返回业务值、CallResult 和最终错误；CallResult 携带裁决／写入证据及已保留材料，其 Err 与尾部错误一致。EthClient view 复用同一执行，仅省略 CallResult：

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

latest／safe／finalized按原level建立调用内reference，显式pin不换块；reference失败不发业务请求，故障不缩冻结分母。pending-local是来源本地观察，多源同值也不升级Corroborated。

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

写入先校验冻结交易字节，再按accepted／unknown／rejected交付证据；未知结果不自动重放。同步写入可按独立合同轮询收据，不代表自动重复广播。库不签名、不选择gas、不分配nonce、不管理替换交易。

root和view提供SubscribeNewHead、SubscribeFilterLogs、SubscribeTransactionReceipts，使用生成模型channel及ethereum.Subscription。多源订阅保持固定WSS快照、物理session检查和同源Pull；多源client仅一个WSS来源会拒绝，显式单源client保留原路径。公开单源策略、预算和终止细化见 [订阅合同](docs/design/subscription-contract.md)。

raw WSS提供ID demux与有界通知；adapter不自动重连／replay，root订阅worker可以重建来源会话。不承诺通用keepalive或无限恢复。HTTP/WSS错误presence、合成测试和真实provider观察分别记账。

任一facade Close停止共享Client，新请求在网络前失败，在途公开操作随owner结束。transport adapter自身Close和订阅Err／channel是不同层，不能把某一层的正常关闭外推为所有层无错误。

## 模块与验证

唯一生产装配位于internal/runtime；rpc拥有通用裁决，methods拥有方法语义，aggregator编排调用，pool拥有成员／收集／健康。ethapi与ethcodec提供模型和原语，objects／ethobject拥有绑定与对象保护；typed只有一份。transport与subscription分别拥有协议和流，工具／测试消费核心而不被核心反向导入。

从 [文档导航](docs/design/README.md)进入完整现行合同。历史decision供追溯，不是另一份执行计划。后续先完成文档基线，再处理内部手写平行和逐模块实现优化，不重做公开类型迁移。

默认测试离线；Anvil与provider采集显式运行。本地链／合成通过不关闭外部缺口；canary passed、workflow成功和发布批准分别表达。正式门禁、固定工具和产物流程仅在 [发布合同](docs/design/release-contract.md)维护。
