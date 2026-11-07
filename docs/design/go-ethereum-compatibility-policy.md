# go-ethereum 版本兼容政策

状态：执行政策（2026-11-07）。

EthGuard 对外的 `EthClient` 契约按仓库锁定的 go-ethereum 版本定义。发布目标不是
“当前已经接入的 ethclient 子集”，而是完整实现该版本 `ethclient.Client` 的业务方法
集合。`v1.17.5` 导出的 48 个 receiver 中，`Client()` 只是返回底层 `*rpc.Client`
的具体类型访问器，不属于 EthGuard 的业务 facade；因此当前锁定版本的兼容目标是
47 个业务方法。当前 `EthClient` 已接入 46 个方法；仅 `SimulateV1` 因默认构建明确禁止
生产根包导入 `go-ethereum/ethclient` 而暂缓，不能把该缺口伪装成已完成。当前集合只是
接入过程中的内部检查点，不能作为最终兼容版本发布或对外能力承诺。

每个兼容版本必须满足：

1. 从锁定的 go-ethereum 源码生成或核对完整 `ethclient.Client` 方法清单，并明确排除
   `Client()` 这个底层 rpc 访问器；
2. `EthClient` 的公开业务方法集合与该清单完全一致，包含 unary、pending、写入派生和
   订阅方法；
3. 每个方法都有 Root execution 映射、typed projection、参数与返回绑定、错误分层、
   生命周期和 HTTP/WSS 证据；订阅方法还必须有 `ethereum.Subscription`、取消、重连、
   背压、异步错误和 race 矩阵；
4. 不支持的方法不能留在最终兼容版本的“已实现”集合中。若上游方法无法实现，发布必须
   阻塞，不能用 `ErrUnsupported` 或未知 `Call` 把子集伪装成完整 client；
5. 升级 go-ethereum 时先生成新旧方法差异，再补齐新增/变更方法和行为测试，最后把
   `go.mod`、方法清单、兼容性测试和发布说明一起更新。

Root `Client` 继续提供带 `CallResult` 的证据型接口和必要的控制面；它可以比官方视图
更丰富，但不替代 `EthClient` 的完整兼容承诺。`CallResult` 只用于单次 unary 调用，
订阅使用独立事件结果和生命周期类型。

`EthClient` 不暴露 `Client()`。调用底层 go-ethereum `*rpc.Client` 会绕过 EthGuard 的
runtime、quorum、typed projection、证据和生命周期边界；需要底层访问的内部 adapter
仍由生产组合根持有。方法差异脚本和 API contract test 都把该访问器标成显式排除项，
不能用它把 47 个业务方法的缺口隐藏起来。

外部反馈只发布一个版本化兼容声明：

```text
go-ethereum: vX.Y.Z
EthClient: complete
Root Client: evidence facade over the same execution
HTTP/WSS: unary + subscription status from the same method matrix
```

内部可以继续按语义族分阶段开发、测试和审计，但阶段名称、当前完成百分比和中间子集不
进入外部兼容版本语义。
