# go-ethereum 版本兼容政策

状态：执行政策（2026-11-09）。

EthGuard 对外的 `EthClient` 契约按仓库锁定的 go-ethereum 版本定义。发布目标不是
“当前已经接入的 ethclient 子集”，而是完整实现该版本 `ethclient.Client` 的业务方法
集合。`v1.17.10` 导出的 48 个 receiver 中，`Client()` 只是返回底层 `*rpc.Client`
的具体类型访问器，不属于 EthGuard 的业务 facade；因此当前锁定版本的兼容目标是
47 个业务方法。当前 `EthClient` 已接入 46 个方法；仅 `SimulateV1` 因默认构建明确禁止
生产根包导入 `go-ethereum/ethclient` 而暂缓，不能把该缺口伪装成已完成。当前集合只是
接入过程中的内部检查点，不能作为最终兼容版本发布或对外能力承诺。

`gethcompat.SimulateClient` 提供了一个显式单源逃生舱：调用方主动导入
`gethcompat` 后，可以使用官方 `ethclient.SimulateOptions`、
`ethclient.SimulateBlockResult` 和 `SimulateV1` 签名。该适配器复用 go-ethereum
`ethclient` 的连接与生命周期，不进入默认根包依赖图，不产生 `CallResult`、quorum
或跨源证据，也不把 `SimulateV1` 计入 `EthClient` 的 47-method 完整性门禁。
因此它解决了官方类型的可访问性，但没有改变完整兼容发布仍被 `SimulateV1` 阻塞的事实。

已核对当前锁定的 go-ethereum `v1.17.10` 官方 `ethclient` 源码：
`SimulateOptions`、`SimulateBlockResult` 与 `SimulateV1` 仍定义在
`github.com/ethereum/go-ethereum/ethclient` 包中。该版本没有解除默认根包的
`ethclient` 导入门禁，因此默认生产拓扑仍按架构阻断处理；需要完整 `EthClient`
兼容时，后续仍须另行决定是否调整 interlock 并为 `SimulateV1` 建立根执行、证据和
生命周期契约。

方法集合计数不证明返回字段已完整兼容。当前 `SyncProgress` 投影仍遗漏上游已有的
`SyncedAccessLists`、`TotalAccessLists`、`TrieGenProgress`、`TxIndexFinishedBlocks`、
`TxIndexRemainingBlocks`、`StateIndexRemaining` 和 `TrienodeIndexRemaining`。
后三项被置零可能使 `Done()` 在索引尚未完成时返回 `true`。这些字段在 `v1.17.5`
已经存在；这是既有行为缺口，不是 `v1.17.10` 升级回归，本次依赖升级未修改该逻辑。

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

## 2026-11-09 依赖升级记录

- go-ethereum 从 `v1.17.5` 升至 `v1.17.10`；其依赖要求将
  `golang.org/x/crypto` 从 `v0.48.0` 带至 `v0.55.0`，无需 `replace`。
- 同批升级保留 Prometheus `client_golang v1.24.1 → v1.25.0`。
  后者最低要求 Go 1.26.0，项目既有 Go 1.26.10 满足该要求；现有指标名与
  label 契约不变，HTTP 导出增加用于内容协商的 `Vary` 响应头。
- 重新运行 `scripts/ethclient-method-diff.sh`：上游仍为 48 个导出方法，
  排除 `Client()` 后为 47 个业务方法；本项目保持 46 个，唯一缺失的方法仍为
  `SimulateV1`。升级不构成完整兼容发布。
- 在本地受控 JSON-RPC 节点上实际运行原生 HTTP/WSS 与 gethcompat HTTP/WSS：
  核验区块解码、EIP-1898 state-hash、既有同步字段、receipt NotFound、revert data、
  官方类型的单源 SimulateV1，以及真实调用产生的指标注册、Gather 与 HTTP 导出。
  这些是本地运行证据，不是外部 provider 或真实 geth 节点的兼容保证。
- 对同一份 `eth_syncing` 响应做实际对照：geth 保留剩余索引计数 `3/2/1`，
  `Done()` 为 `false`；EthGuard 投影成 `0/0/0`，`Done()` 为 `true`。
  此结果单独记录既有缺口，不计作升级通过项。
- 升级后的全仓普通测试、race 包内覆盖率和 race 跨包覆盖率均通过（44 个测试包）；
  build、vet、golangci-lint、staticcheck、模块校验和 `go mod tidy -diff` 均通过。
  使用仓库锁定的 `govulncheck v1.9.0` 扫描，结果为 `No vulnerabilities found.`。
