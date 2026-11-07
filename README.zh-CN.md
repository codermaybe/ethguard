# EthGuard

[English](README.md) · **简体中文**

> 状态：早期实现阶段。对外兼容目标是 `go.mod` 锁定的 go-ethereum 版本对应的
> 完整 `ethclient` 方法集合；当前代码仍在追平该目标，不能把中间子集当作完整
> 兼容版本发布。

EthGuard 是一个面向多源 Ethereum JSON-RPC 的聚合客户端。它查询多个上游，
依据固定的 Ethereum execution-apis 契约校验响应，清洗官方标准字段，并聚合
受支持的扩展字段。官方字段构成受保护的核心结果；特性字段和厂商字段按照各自
的方法契约作为扩展数据保留。客户端通过跨源一致性评估响应可靠性，并提供带有
状态和诊断证据字段的结果。

EthGuard 优先考虑相对可靠性，而不是性能和低延迟。对于需要移动参考的读取，
它会先建立一个由多个上游提供证据的共同参考，再评估业务响应。`ethguard.Client`
的类型化方法保持 `go-ethereum/ethclient` 的方法名、参数、返回类型和调用方式，因此普通调用
可以保持相同形态，不需要调用方显式编排 anchor/confirm/read 流程。跨源一致性是
provider 提供的证据，不是 Ethereum 共识、规范链证明、密码学最终性或轻客户端
验证；多个 URL 也不代表彼此独立。

## 目录

```text
ethguard/                   # 公共 Client 外观
├── client.go               # 公共 Client facade、New、Pool、Close
├── call.go                 # 通用只读 Call 入口和解码委托（CallInto 已删除）
├── eth.go                  # 类型化便捷方法
├── anchor.go               # 移动标签读取背后的锚定策略
├── write.go                # 写入流水线（独立的裁决流程）
├── errors.go               # 分类哨兵错误
├── uncertain.go            # 按调用选择的捕获（包括厂商方言证据）
├── config/                 # Go 配置和 YAML 配置
├── pool/                   # 上游生命周期、Transport Port、健康度和准入资格
├── observe/                # 被动可观测性接口
│   └── prometheus/         # 可选指标适配器（不放入核心）
├── aggregator/             # Validate → Reference → Collect → Admit → Resolve → Project
├── rpc/                    # 公共请求、裁决、计划、策略和证据模型
│   ├── projection/         # 公共标准字段投影和扩展 Carrier
│   ├── methods/            # 具体 Ethereum JSON-RPC 方法和目录
│   │   ├── eth_*.go        # 各方法常量、策略和装配
│   │   ├── block.go        # 区块方法策略和方法级准入
│   │   ├── logs.go         # 日志请求/响应绑定和组装
│   │   └── catalog.go      # 方法覆盖清单
│   └── internal/           # rpc 和 methods 使用的实现包
│       ├── wire/           # JSON 规范化和按规范裁剪
│       ├── objects/        # 区块、收据、交易、日志、feeHistory 规则
│       └── policy/         # 共享准入装饰器
├── internal/
│   ├── runtime/            # 唯一 Composition Root：plan、projector、aggregator 装配
│   ├── typed/              # 唯一生产 typed projection；root 类型化读取共用
│   ├── jsonsafe/           # 有界 JSON 校验（重复键/大小限制）
│   ├── reference/          # reference 建立和 Head-Reading Port
│   ├── refcontext/         # 每次调用的 reference 传播
│   ├── uncertain/          # 按调用捕获原始证据
│   ├── chainlist/          # chain list 数据和加载
│   ├── rpcsample/          # 采样计划和记录支持
│   ├── spec/               # 固定的规范参考数据（不是手写代码）
│       ├── VERSION         # execution-apis 版本固定点，唯一来源
│       ├── methods.json    # 精简后的方法结果契约（约 100KB）
│       ├── vectors/        # 从 tests/*.io 提取的符合性向量
│       └── README.md       # 来源、升级流程和审计指南
│   ├── verification/       # 面向公开 seam 的跨模块验证
│   │   ├── facade/         # root Client/EthClient 黑盒验证
│   │   └── integration/    # 跨模块集成验证
│   ├── transport/          # Transport Port 适配器和契约测试
│   │   ├── http/            # 项目自有 native HTTP 适配器
│   │   ├── wss/             # 项目自有 raw WebSocket 调用适配器
│   │   ├── geth/            # 显式 go-ethereum 兼容适配器
│   │   └── contract/        # transport 无关的 L1 契约套件
│   └── testkit/wstest/      # WSS 服务端测试对端
├── gethcompat/              # go-ethereum transport 的显式构造 seam
├── rpctest/                # 基于真实 HTTP 的进程内可编排 Ethereum 节点测试工具
├── cmd/                    # 可执行入口（含 wsscapture、livecanary）
├── docs/                   # 架构、威胁模型和设计说明
│   └── design/             # 已接受的架构和聚合记录
└── examples/               # 简单导入示例
```

| 包 | 作用 |
| --- | --- |
| `ethguard` | 唯一公开 `Client`：Geth 形态类型化读取、通用调用、证据与写入 |
| `ethguard/config` | Go 构造和 YAML 加载共用的一套配置模型 |
| `ethguard/pool` | 共享上游连接、Transport Port、采集、健康度和准入资格 |
| `ethguard/observe` | 可观测性事件（`Recorder`），不参与安全决策 |
| `ethguard/aggregator` | 语义门槛、冲突和证据 |
| `ethguard/rpc` | 公共请求、裁决、ReadPlan、resolver、观察和证据模型 |
| `ethguard/rpc/projection` | 标准字段投影和扩展 Carrier 组装 |
| `ethguard/rpc/methods` | 具体 Ethereum JSON-RPC 方法、策略和目录 |
| `ethguard/rpc/internal` | 内部 wire、对象和准入实现 |
| `ethguard/internal/runtime` | 唯一 Composition Root，绑定 methods、projector 和 aggregator |
| `ethguard/internal/typed` | 唯一生产 typed decoder/projection |
| `ethguard/internal/reference` | reference 算法和 Head-Reading Port |
| `ethguard/internal/verification` | 面向公开 seam 的契约、场景、集成和回放验证 |
| `ethguard/internal/spec` | 固定的 execution-apis 参考数据（版本、schema、向量） |
| `ethguard/rpctest` | 基于真实 HTTP 的可编排多源测试工具 |
| `ethguard/internal/transport` | Transport Port 实现和中立契约测试 |
| `ethguard/gethcompat` | 显式 go-ethereum 兼容构造 seam |

并发模型：每个进程用例共享一个 `Client` / `pool.Pool`；上游保持长连接。调用方
不应为每个请求重新拨号，也不应为每个 goroutine 克隆 pool。Client 生命周期结束
时应由所有者调用 `Close`。

分层规则：`internal/runtime` 是唯一 Composition Root，负责把方法清单编译成
ReadPlan，并绑定 plan、projector、aggregator 和 reference seam；根 `Client` 是
其上的唯一公开表面。`aggregator` 消费 ReadPlan 以及 resolver/admission
接口，不导入 `methods`；`rpc` 也不导入 `methods`。Ethereum 方法入口和 MethodSpec
清单位于 `rpc/methods`；共享的 wire 规范化、裁剪、对象语义和准入辅助位于
`rpc/internal`。
`pool` 定义最小 Transport Port，`internal/reference` 定义 Head-Reading Port，
两者都不承载方法 policy 组装或业务裁决。标准投影通过 `rpc/projection` 暴露。

- **R1 – 切片准入。** 只由一个方法使用的形状（feeHistory、syncing、accessList、
  call/estimateGas、quantity 读取）留在 `eth_*.go` 中；只有被至少两个方法共享或
  携带密码学承诺的对象才建立切片。
- **R2 – 表级所有权。** 定义切片独占其字段表；调用方复用而不复制（`logs.go`
  所有由 `receipt.go` 使用的 `Log` 表）。实现可以在切片变得难以维护时在同一包
  内拆分，但字段表仍只归一个文件所有，不预先拆分。
- **R3 – 数据而非生成代码。** `internal/spec` 保存固定的 execution-apis 参考数据
  （VERSION、精简的 `methods.json`、符合性向量），除了 embed accessor 外不包含
  手写逻辑。白名单树在 init 时由同一个 bundle 编译，测试也校验该 bundle；没有
  生成 Go 代码和新鲜度缺口。生产 typed 解码位于 `internal/typed`，支撑根
  `Client` 的类型化读取；严格的 requiredness 仍由各切片按 fork 手写。
- **R4 – 清单互锁。** 测试确保 `catalog.go` 中的每个已注册方法都在
  `spec_tables.go` 中有 schema 映射。
- **Anchor 分层。** anchor facade 只查询：`Client` 拥有 runtime 构建的
  resolver，并从根 `anchor.go` 暴露 `AnchorBlockHash`/`AnchorBlockHashForTag`/
  `AnchorBlockHeight`；reference 算法和 Head-Reading Port 位于
  `internal/reference`，调用内证据传播位于 `internal/refcontext`（`Capture`）。
- **双切面、单执行、四层证据。** `ethguard.Client` 拥有全部业务方法，每个操作返回
  `CallResult`：业务值由方法自身返回携带，决策（或写决策）与已采集材料在结果上，
  `CallResult.Err` 是调用的最终业务结论。ethclient 形状的 `EthClient` 视图以官方
  签名投影同一执行。四条证据通道相互独立：(1) 类型化分类错误加结果上的决策证据，
  (2) 通用只读 `Call`（成功 JSON 在决策 Value 上），(3) 按调用可选的
  `WithResult`/`WithSourceEvidence` 原始 source 材料（每次调用独立预算），(4) 审计
  与运维用的 `observe.Recorder` 事件。recorder 绝不是同步业务返回通道。

### 执行模型与覆盖范围

默认 runtime 为每个已注册读取方法编译一个 `MethodSpec → rpc.ReadPlan`。plan
持有规范化、reference/pending、准入、resolver 选择和 projection 绑定等阶段事实。
执行骨架是：

```text
Validate / Prepare → Reference → Collect → Admit → Resolve → Project
```

当前清单包含 26 个已注册读取方法。根 `Client` 在该清单之上提供 Geth 形态的
类型化方法和通用 `Call`。`eth_getProof` 明确为 unsupported。计划已注册不代表真实
provider 的兼容性已经完成，provider 证据仍是独立轨道。

旧的逐方法 Policy 构造器保留为 resolver seam、兼容表面和等价测试 oracle；它们不是
默认 runtime registry，也不能形成第二个组合根。方法级编排已经计划化；strict、median、
local、call-revert 和 block-specific policy 仍是有意保留的深模块。

## 使用

支持的入口：两个或更多互异 endpoint，默认配置：

```go
client, err := ethguard.Dial("https://your-provider-a", "https://your-provider-b")
if err != nil { return err }
defer client.Close()

balance, _, err := client.BalanceAt(ctx, account, nil)
```

单 endpoint 必须显式进入 Forwarded 模式——`Dial` 拒绝单个 URL，重复 URL 也会
失败（重复只会缩小投票基数，不增加来源）：

```go
client, err := ethguard.DialUncorroborated("https://your-provider")
```

以 YAML 配置 EthGuard 的运维入口直接加载文件：

```go
client, err := ethguard.LoadFile("config.yaml")
```

`config.yaml` 可以是普通的 YAML 配置文件：

```yaml
chain_id: 1
upstreams:
  - id: primary
    endpoint: https://your-provider-a
  - id: secondary
    endpoint: https://your-provider-b
```

高级控制（锚定 preset、tip 跟踪、collect 窗口）以编程方式构建配置并用 `New`
构造：

```go
cfg, err := config.FromEndpoints("https://a.example:8545", "https://b.example:8545")
if err != nil { return err }
client, err := ethguard.New(cfg, ethguard.WithFinalizedAnchoring())
```

`Dial` 不会隐式设置链身份——用 `ethguard.WithChainID(chainID)` 显式固定（Go
option 赢过文件中的 `chain_id`）。构造保持 lazy：首次调用前不联系任何
endpoint，`DialContext` 的 context 只约束构造本身。

保存该文件后，可以运行示例：

```bash
go run ./examples/basic -config ./config.yaml
```

`ethguard.Client` 是承载裁决证据的根接口，不是可有可无的演示。通用 `Call`、完整
裁决证据和 pool 检查都在同一个 client 上。对外兼容目标是 `go.mod` 锁定版本对应的
完整 `ethclient` 方法集合；当前代码仍是追平该矩阵的内部检查点。

确定性类型化读取需要库内部动态计算的交叉验证门槛：支持不足时返回 `ErrConflict`，
因为这些方法不能向调用方暴露 `Selected`。在方法契约允许时，高级根 `Call` 仍会返回
selected 状态。Median/local 方法保留其选择语义。`Call` 在本地拒绝未知方法，
并返回 `ErrInvalidRequest`。

使用 finalized anchoring 时，不同上游的区块高度会被协调到一个共同高度。已经匹配的
头会复用；否则通过一次有界的确认轮次补齐缺失响应。默认一致性要求第一轮的新鲜视图
确认该候选；内部计算的支持门槛在确认期间保持第一轮的参与分母。确认失败不会缩小该
分母，也不会静默降低要求。`Decision.Reference` 会在业务答案证据之外保留 reference
的来源、区块高度、哈希、参与者、支持者和故障信息。发布的证据持有可变切片的独立副本。

这可能增加一次区块头查询；reference 支持不足会在业务读取前失败。reference 的建立
由库负责，并在证据中保留请求的移动标签。finalized 覆盖仍是上游报告的声明，不是独立
的 Ethereum 共识最终性。

完整区块读取（带完整交易的 `eth_getBlockByNumber` 和 `eth_getBlockByHash`）会在响应
参与投票前校验请求绑定和协议承诺：区块头哈希、交易 trie root、适用的 withdrawals
root 以及 uncle commitment。历史 uncle header 从同一来源获取，并保留在类型化区块中。
这保护内容与区块头的一致性，但不证明规范链归属或链上最终性。真实上游和 fork 兼容性
样本仍是独立的交叉验证步骤。

结果交付与原始来源材料按调用选择，并且只属于一次调用。`WithResult` 在 context 上
放置一个全新的 capture——被绑定的调用只在那里发布一次完成结果——`WithSourceEvidence`
为派生 context 上的每次调用配置原始上游材料的保留预算：

```go
base, err := ethguard.WithSourceEvidence(ctx, 64<<10) // 一次调用可保留的总字节数
if err != nil { return err }                          // 非正预算本地失败，不触网
callCtx, capture := ethguard.WithResult(base)
block, _, err := client.BlockByNumber(callCtx, nil)
_ = block // 普通结果和错误保持原有语义
result, published, err := capture.Result()
if published {
	for _, field := range result.Uncertain.FieldsFor("vendorField") {
		log.Printf("%s %s: %s", field.Source.ID, field.Path, field.Value)
	}
	for _, response := range result.Uncertain.Raw {
		log.Printf("%s %s: %s", response.Source.ID, response.Method, response.Error)
	}
	if result.Uncertain.Truncated {
		log.Printf("材料截断: retained=%d dropped=%d", result.Uncertain.Bytes, result.Uncertain.Dropped)
	}
}
```

`result.Err` 与尾随错误一致；`result.Decision` 携带该调用实际形成的聚合事实。聚合
失败清空公开值但来源表保留；协证收据 null 与协证 revert 保留载荷并携带分类业务
错误。本版保留规则固定：参数指定的总字节预算、每条原始响应最多 64 KiB、Raw 与
Fields 合计最多 256 条记录、每项可变元数据文本最多 4 KiB。预算耗尽只停止保留——
绝不取消请求、法定人数或投票——transport 响应上限保持独立。进入第二次独立调用的
capture 永久失效（`ErrCaptureReused`）；经 panic 异常退出的调用把 capture 标记为
异常（`ErrCaptureAborted`）。捕获错误是交付事实，永不进入业务错误。

ethclient 形状的视图走同一执行：`client.EthClient()` 暴露官方读取签名、同步写入、
三个订阅方法和 `Close`。它满足
`bind/v2.ContractCaller` 与 `bind/v2.DeployBackend`；它不是 `*ethclient.Client`。
未绑定 capture 的视图调用省略原始材料采集（没有消费者）；业务行为不变。任一切面上
的一次 `Close` 同时停止两者：在途操作以可识别的 `ErrPoolClosed` 原因收敛，新操作在
任何网络接触之前失败。

### HTTP 与 WSS 来源

每个 source 显式选择 transport；省略时仍使用 native HTTP。`transport: wss`
启用 raw WebSocket adapter，并要求 `ws://` 或 `wss://` endpoint：

```yaml
upstreams:
  - id: provider-a
    endpoint: https://rpc-a.example
  - id: provider-b
    endpoint: wss://rpc-b.example
    transport: wss
```

两者都作为普通固定成员参与读取和交易广播，不新增只读角色。方法不支持是本次
请求的协议证据，不是全局 health failure；不提供结果支持票，也不降低门槛。
限流及可归因的 transport/server failure 影响共享读写 health。未知交易结果不
自动重放，仅后续独立调用可惰性重拨。raw WSS 保留 missing/null/`0x`/具体 error
data，不等于真实 provider 普遍兼容性已验证。同一 backend 的 HTTP/WSS endpoint
不构成两个独立信任来源。显式 per-source transport 不能与 `DialerOption` 或
`gethcompat.New` 混用。

raw WSS 是 EthGuard 专用的调用 transport，提供受限的订阅通知能力，不是通用 WebSocket 客户端。根订阅按 source 重订阅并检查 generation；transport 层 keepalive 及通用队列/重放策略仍待独立运维证据。

参见[架构入口](docs/architecture.md)、[代码库拓扑](docs/design/codebase-topology.md)、
[go-ethereum 兼容政策](docs/design/go-ethereum-compatibility-policy.md)和[威胁模型](docs/threat-model.md)，
了解安全属性和方法边界。

所有来源都被视为相同的投票成员。EthGuard 不建模 provider、区域、客户端家族或权重
带来的独立性；匹配结果是 provider 故障容忍证据，而不是 Ethereum 链共识、规范链归属
或独立最终性的证明。

## 验证证据

- **离线回归**：`go test`、进程内 `rpctest` fleet 和 replay 证明固定输入下的确定性
  行为，不证明真实 provider 行为、方言覆盖或长期可用性。
- **形状观察**：历史 capture 和 `cmd/soak` 探针是某一天、某一 fleet 的未认证观察，
  不是外部 live 证据、attestation 或兼容性保证。
- **Live canary**：`cmd/livecanary` 固定 Chainlist 快照、voting basis、请求计划和失败
  分类，并生成带 digest 绑定的机器报告。`passed` 只表示本次 run 满足契约；
  `inconclusive` 不是成功。现有一次本地诊断、首次外部观察（run `39983945116`）与
  独立 workflow 对同一 manifest 的复核（run `40078416681`，`repeatable=true`，
  差异归为 provider 波动）。这些观察均为 `inconclusive`，不构成兼容性、共识、
  finality、provider 独立性或长期可靠性证明。
- `latest` / `safe` / `finalized` 是 provider 报告的 tag，不是 Ethereum finality。
  `Corroborated` 是 provider-backed 跨来源证据，不是共识或规范链证明。

库提供：配置加载；带惰性拨号和健康恢复的多上游生命周期；基于固定调用分母动态计算
的交叉验证和确定性冲突证据；区块锚定读取（严格或保守的 finalized 选择）；隔离落后
来源的新鲜度闸门；分类哨兵错误；以及带可选 Prometheus adapter 的被动可观测性接口。
当前支持的方法集支持交叉验证；未交叉验证的方法会被转发并标记为 `Forwarded`，不会被
声称为已交叉验证。

EthGuard 可以评估 gas estimate、fee 数据和交易计数等 RPC 观察结果。它不负责 gas 优化、
nonce 分配、交易签名，也不负责交易广播和替换管理。
