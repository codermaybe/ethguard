# EthGuard

[English](README.md) · **简体中文**

> 状态：早期实现阶段。核心聚合模型和主要执行路径已经建立，但方法覆盖、
> API 边界和实现质量仍在持续完善。

EthGuard 是一个面向多源 Ethereum JSON-RPC 的聚合客户端。它查询多个上游，
依据固定的 Ethereum execution-apis 契约校验响应，清洗官方标准字段，并聚合
受支持的扩展字段。官方字段构成受保护的核心结果；特性字段和厂商字段按照各自
的方法契约作为扩展数据保留。客户端通过跨源一致性评估响应可靠性，并提供带有
状态和诊断证据字段的结果。

EthGuard 优先考虑相对可靠性，而不是性能和低延迟。对于需要移动参考的读取，
它会先建立一个由多个上游提供证据的共同参考，再评估业务响应。`ethguard/ethadapter`
保持 `go-ethereum/ethclient` 的方法名、参数、返回类型和调用方式，因此普通调用
可以保持相同形态，不需要调用方显式编排 anchor/confirm/read 流程。跨源一致性是
provider 提供的证据，不是 Ethereum 共识、规范链证明、密码学最终性或轻客户端
验证；多个 URL 也不代表彼此独立。

## 目录

```text
ethguard/                   # 公共 Client 外观
├── client.go               # 组合根：Client、New、Pool、Close
├── call.go                 # 通用调用流水线和类型化解码（Call、CallInto[T]）
├── eth.go                  # 类型化便捷方法
├── anchor.go               # 移动标签读取背后的锚定策略
├── write.go                # 写入流水线（独立的裁决流程）
├── errors.go               # 分类哨兵错误
├── uncertain.go            # 按调用选择的捕获（包括厂商方言证据）
├── config/                 # Go 配置和 YAML 配置
├── pool/                   # 上游生命周期、健康度、tip 和准入资格
├── observe/                # 被动可观测性接口
│   └── prometheus/         # 可选指标适配器（不放入核心）
├── aggregator/             # 只负责编排：Collect → Admit → Resolve
├── rpc/                    # 公共请求、裁决、策略和证据模型
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
│   ├── jsonsafe/           # 有界 JSON 校验（重复键/大小限制）
│   ├── reference/          # 内部 finalized/reference 建立
│   ├── refcontext/         # 每次调用的 reference 传播
│   ├── uncertain/          # 按调用捕获原始证据
│   ├── chainlist/          # chain list 数据和加载
│   ├── rpcsample/          # 采样计划和记录支持
│   └── spec/               # 固定的规范参考数据（不是手写代码）
│       ├── VERSION         # execution-apis 版本固定点，唯一来源
│       ├── methods.json    # 精简后的方法结果契约（约 100KB）
│       ├── vectors/        # 从 tests/*.io 提取的符合性向量
│       └── README.md       # 来源、升级流程和审计指南
├── ethadapter/             # 面向 ethclient 的类型化接口
├── rpctest/                # 基于真实 HTTP 的进程内可编排 Ethereum 节点测试工具
├── cmd/                    # 可执行入口（soak、rpcsample、shapeprobe、specbundle、specdiff）
├── docs/                   # 架构、威胁模型和设计说明
│   └── design/             # 已接受的架构和聚合记录
└── examples/               # 简单导入示例
```

| 包 | 作用 |
| --- | --- |
| `ethguard/ethadapter` | 支持方法的主要 ethclient 形态客户端 |
| `ethguard` | 高级 `Client`、组合根、通用调用和证据 |
| `ethguard/config` | Go 构造和 YAML 加载共用的一套配置模型 |
| `ethguard/pool` | 共享上游连接、采集、健康度、tip 和准入资格 |
| `ethguard/observe` | 可观测性事件（`Recorder`），不参与安全决策 |
| `ethguard/aggregator` | 语义门槛、冲突和证据 |
| `ethguard/rpc` | 公共请求、裁决、策略、观察和证据模型 |
| `ethguard/rpc/projection` | 标准字段投影和扩展 Carrier 组装 |
| `ethguard/rpc/methods` | 具体 Ethereum JSON-RPC 方法、策略和目录 |
| `ethguard/rpc/internal` | 内部 wire、对象和准入实现 |
| `ethguard/internal/spec` | 固定的 execution-apis 参考数据（版本、schema、向量） |
| `ethguard/rpctest` | 基于真实 HTTP 的可编排多源测试工具 |

并发模型：每个进程用例共享一个 `Client` / `pool.Pool`；上游保持长连接。调用方
不应为每个请求重新拨号，也不应为每个 goroutine 克隆 pool。Client 生命周期结束
时应由所有者调用 `Close`。

分层规则：`aggregator` 只了解 `rpc.Policy` / `AdmissionPolicy` 接口，不导入
`methods`；`rpc` 也不导入 `methods`（已通过 `go build ./...` 保持无循环依赖）。
Ethereum 方法入口和方法级绑定位于 `rpc/methods`。共享的 wire 规范化、裁剪、对象
语义和准入辅助位于 `rpc/internal`；标准投影通过 `rpc/projection` 暴露。

- **R1 – 切片准入。** 只由一个方法使用的形状（feeHistory、syncing、accessList、
  call/estimateGas、quantity 读取）留在 `eth_*.go` 中；只有被至少两个方法共享或
  携带密码学承诺的对象才建立切片。
- **R2 – 表级所有权。** 定义切片独占其字段表；调用方复用而不复制（`logs.go`
  所有由 `receipt.go` 使用的 `Log` 表）。实现可以在切片变得难以维护时在同一包
  内拆分，但字段表仍只归一个文件所有，不预先拆分。
- **R3 – 数据而非生成代码。** `internal/spec` 保存固定的 execution-apis 参考数据
  （VERSION、精简的 `methods.json`、符合性向量），除了 embed accessor 外不包含
  手写逻辑。白名单树在 init 时由同一个 bundle 编译，测试也校验该 bundle；没有
  生成 Go 代码和新鲜度缺口。解码（`call.go`）和准入共享编译后的 mask；严格的
  requiredness 仍由各切片按 fork 手写。
- **R4 – 清单互锁。** 测试确保 `catalog.go` 中的每个已注册方法都在
  `spec_tables.go` 中有 schema 映射。
- **Anchor 分层。** 公共 anchor 接口（`ReferenceResolver`、`StrictHeadAnchor`、
  options）位于根目录 `anchor.go`；具体机制位于 `internal/reference` 和
  `internal/refcontext`。
- **两种类型化接口。** `eth.go`（根 `Client`）为高级调用方返回带有 `rpc.Decision`
  证据的值；`ethadapter` 返回 ethclient 形态的值供直接替换使用。adapter 只做
  形状转换并委托根能力，不能再实现第二套解码器。

## 使用

```go
cfg, err := config.LoadFile("config.yaml")
if err != nil { return err }
client, err := ethadapter.New(cfg)
if err != nil { return err }
defer client.Close()

balance, err := client.BalanceAt(ctx, account, nil)
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

保存该文件后，可以运行示例：

```bash
go run ./examples/basic -config ./config.yaml
```

`ethadapter` 是主要的支持方法接口，不是可有可无的演示。`Unwrap()` 暴露高级的
`ethguard.Client`，用于通用 `Call`、裁决证据和 pool 检查。adapter 只对支持的子集
提供保持方法形态的兼容性，并不声称实现完整的 `ethclient` 接口。

确定性 adapter 读取需要库内部动态计算的交叉验证门槛：支持不足时返回 `ErrConflict`，
因为这些方法不能向调用方暴露 `Selected`。在方法契约允许时，高级根 `Call` 仍会返回
selected 状态。Median/local 方法保留其选择语义，未知方法继续转发。`Wrap` 与 `New`
执行相同的安全检查。

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

不确定数据按调用选择，并且只属于一次调用。普通方法签名保持不变；传入由
`WithUncertain` 创建的 context，然后在成功或失败后读取稳定的防御性快照：

```go
callCtx, uncertain := ethguard.WithUncertain(ctx)
block, err := client.BlockByNumber(callCtx, number)
_ = block // 普通结果和错误保持原有语义
snapshot := uncertain.Result()
for _, field := range uncertain.Fields("vendorField") {
	log.Printf("%s %s: %s", field.Source.ID, field.Path, field.Value)
}
for _, response := range uncertain.Responses() {
	log.Printf("%s %s: %s", response.Source.ID, response.Method, response.Error)
}
if snapshot.Truncated {
	log.Printf("uncertain data truncated: retained=%d dropped=%d", snapshot.Bytes, snapshot.Dropped)
}
```

字段会保留每个来源的原始值，包括相同值和显式 `null`；在预算允许时，格式错误的
响应以及传输/协议错误也会保留在 response 记录中。这些数据是兼容性证据，不是可靠性
保证，也不是最近客户端缓存。采集期间会限制保留量；取消操作可能留下不完整的快照。
每个逻辑调用都需要重新捕获；返回值是防御性副本。HTTP 捕获会保留 error data 缺失
与 JSON `null` 的区别；WSS 路径无法区分这两种情况。

参见[架构记录](docs/design/architecture-record.md)、[架构说明](docs/architecture.md)、
[聚合概览(历史综述)](docs/design/rpc-aggregation.md)、[proxyd 对比](docs/design/proxyd-comparison.md)
和[威胁模型](docs/threat-model.md)，了解安全属性和方法边界。

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
  `inconclusive` 不是成功。2026-10-23 的首次运行是本地诊断，因可用性 fault 为
  `inconclusive`；尚不存在外部发布、可 attest 的 live artifact。
- `latest` / `safe` / `finalized` 是 provider 报告的 tag，不是 Ethereum finality。
  `Corroborated` 是 provider-backed 跨来源证据，不是共识或规范链证明。

库提供：配置加载；带惰性拨号和健康恢复的多上游生命周期；基于固定调用分母动态计算
的交叉验证和确定性冲突证据；区块锚定读取（严格或保守的 finalized 选择）；隔离落后
来源的新鲜度闸门；分类哨兵错误；以及带可选 Prometheus adapter 的被动可观测性接口。
当前支持的方法集支持交叉验证；未交叉验证的方法会被转发并标记为 `Forwarded`，不会被
声称为已交叉验证。

EthGuard 可以评估 gas estimate、fee 数据和交易计数等 RPC 观察结果。它不负责 gas 优化、
nonce 分配、交易签名，也不负责交易广播和替换管理。
