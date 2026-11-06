# EthGuard 外部 RPC 与长连接测试框架

状态：**设计提案（2026-11-04）**。本文只定义测试证据的分层、工具边界和验收
规则，不改变生产代码、Decision、quorum、reference、typed projection 或方法数量。
实现工作应在切片 9–15 完成并通过审计后，按本文单独排期。

## 1. 目标与判断

EthGuard 需要同时验证两类事实：

1. Go 客户端对 JSON-RPC、HTTP、WSS 和连接生命周期的实现是否正确；
2. 真实节点会返回的非标准、缺字段、延迟、截断和错误响应，是否能沿着现有
   `ReadPlan → pool → aggregator → Decision` 路径得到正确分类。

单一的静态 JSON 回放不能承担这两个目标。手写响应表可以验证某个分支，却不能
证明响应来自真实的以太坊状态，也不能覆盖长连接的帧和生命周期行为。测试框架采用
四层证据：

```text
Go contract tests
        │ 传输、帧、字段和 Decision 不变量
        ▼
本地协议场景
        │ httptest / RoundTripper / wstest / net.Pipe
        ▼
Anvil fork 语义场景
        │ 固定 chain、block 和可复现状态
        ▼
livecanary 外部观察
        │ 真实 provider 的当前行为和 provenance
```

每一层证明的范围不同。Anvil 证明“客户端面对真实链状态和合法 JSON-RPC 语义时的
行为”；故障注入证明“客户端面对异常传输或异常 payload 时的行为”；livecanary
才证明某个外部 provider 在某个时间点实际返回过什么。任何一层都不能替代另一层。

## 2. 工具选择

### 2.1 默认使用的工具

| 工具 | 责任 | 选择原因 |
| --- | --- | --- |
| `testing`、`httptest`、`net/http` | HTTP 对端、响应替换、超时和生命周期 | Go 标准库，进程内、确定性、无需端口服务依赖 |
| `http.RoundTripper` | 在真实 HTTP transport 外包一层故障变换 | 不改变 Client API，不需要另写代理服务 |
| `net.Pipe` | 连接 seam 接受 `net.Conn` 时模拟半关闭、阻塞和写取消 | 标准库全双工内存连接，适合边界时序测试 |
| `internal/testkit/wstest` | WSS Upgrade、帧、文本 payload 和 Close 场景 | 仓库已有测试对端，避免建立第二套 WebSocket 测试设施 |
| Anvil | 固定 fork 状态、区块高度和合法以太坊 RPC 语义 | 真实 EVM/区块状态比手写响应更接近业务现场 |
| `livecanary` | 访问真实 provider、记录 manifest、报告和重跑结果 | 现有外部证据流程，带来源、时间和摘要校验 |
| `rpcsample/replay` | 回放已捕获的 provider 观察，验证捕获格式和差异报告 | 作为历史观察和审计资产，不冒充永久链语义证明 |

### 2.2 暂不引入的工具

默认测试不引入 go-vcr、Toxiproxy、Hardhat、Geth RPC server、testify、随机属性
测试框架或 goroutine 泄漏框架。它们要么重复现有标准库 seam，要么会把进程、网络
端口或第三方运行时带入普通 CI。只有出现明确的测试缺口，且现有 seam 无法表达时，
才单独提交工具评估和迁移方案。

go-ethereum 继续作为类型和兼容行为的版本化对照；它不作为本项目的默认 RPC 对端。
EthGuard 要验证 provider 差异和传输故障，单一 Geth server 不能代表这些外部差异。

## 3. 测试拓扑

```text
                         ┌──────────────────────────┐
                         │ livecanary / 外部 provider │
                         │ 手动或定时，产生观察证据    │
                         └─────────────┬────────────┘
                                       │ manifest / report
                                       ▼
┌──────────────┐   HTTP   ┌─────────────────────┐   HTTP   ┌──────────────┐
│              │─────────▶│ httptest + fault     │────────▶│ Anvil fork   │
│  EthGuard    │           │ RoundTripper/Proxy  │          │ fixed block  │
│              │           └─────────────────────┘          └──────────────┘
│              │
│              │   WSS    ┌─────────────────────┐
│              │─────────▶│ wstest scenario peer │
└──────────────┘           │ frame/payload/close  │
                           └─────────────────────┘
                                       │
                           optional net.Pipe at conn seam
```

HTTP 故障默认在 `RoundTripper` 层注入。只有需要验证真实 HTTP 代理拓扑时，才使用
`httputil.ReverseProxy` 把请求转发到 Anvil。WSS 不建立通用反向代理；握手、帧和
连接故障由 `wstest` 直接驱动，业务 payload 通过发送原始 JSON 文本覆盖。

## 4. Anvil fork 语义测试

### 4.1 Anvil 的定位

Anvil 是**本地链语义测试节点**，不是 provider 兼容性证据，也不是每次普通单测的
必需服务。测试可从固定 fork URL 和固定 block 启动 Anvil，并通过实际 JSON-RPC
请求读取余额、代码、存储、历史块、交易收据、fee history、EIP-1898 对象等状态。

启动器应只负责：

- 检查 `anvil` 是否存在；缺失时对 Anvil 专项测试使用 `t.Skip`，不能让普通 CI 失败；
- 使用固定 chain ID、fork URL 和 fork block；
- 使用临时端口，等待 `eth_chainId` 和 `eth_blockNumber` 就绪；
- 在 `t.Cleanup` 中关闭进程；
- 记录 Anvil 版本、fork URL 的脱敏标识、chain ID、block number、block hash 和
  启动参数摘要。

不提交含账号密钥、访问 token 或未脱敏 URL 的启动日志。测试只读链状态；需要状态
变化时，使用 Anvil 自己的 snapshot/revert 能力在单个测试内恢复，不发送真实交易。

### 4.2 生成物与可复现性

Anvil 生成的状态快照可以作为本地测试资产，但必须附带生成 manifest：

```json
{
  "chain_id": 1,
  "fork_block": 18000000,
  "block_hash": "0x…",
  "anvil_version": "…",
  "source_kind": "fork",
  "command_digest": "sha256:…",
  "state_digest": "sha256:…"
}
```

快照是“由真实链状态生成的可执行 fixture”，不是 provider golden。重新生成必须
能够比较 block、版本和 digest；不能在没有记录来源的情况下覆盖旧状态。若 fork
缓存或快照不可用，专项测试应报告不可执行或跳过，不能静默退化为手写 JSON。

### 4.3 Anvil 场景边界

Anvil 适合验证：

- 合法区块和账户语义；
- 固定历史高度、移动 tag 和 EIP-1898；
- 不同请求在同一状态下的结果关系；
- 本地状态修改后的可预测变化；
- 两个固定状态节点之间的分歧。

Anvil 不证明：

- Infura、Alchemy、QuickNode 等 provider 的字段扩展；
- provider 特有的限流、HTTP 状态码和错误 envelope；
- 真实公网 WSS 的代理、负载均衡和断连行为；
- 某个时间点没有观察到的 `error.data: null`。

这些缺口分别由 fault scenario 和 livecanary 负责。

## 5. HTTP 故障场景

HTTP 场景使用业务命名，每个场景必须说明请求方法、来源、故障层、预期 Decision
和证据变化。建议的最小场景集如下：

| 场景 | 注入位置 | 验证目标 |
| --- | --- | --- |
| `http-status-rate-limited` | HTTP status 429 | 限流分类、health/cooldown，不误判为业务值 |
| `jsonrpc-error-data-missing` | JSON-RPC error 缺少 `data` | missing presence 保留并进入正确 fault path |
| `jsonrpc-error-data-null` | `data: null` | null presence 不被当成缺失或空字节 |
| `jsonrpc-error-data-empty-hex` | `data: "0x"` | 空 hex 与 null/missing 分离 |
| `jsonrpc-error-data-value` | `data: "0x…"` | 有值 revert/error data 保留 |
| `http-malformed-json` | body 截断或非法 JSON | 解码失败、source fault 和 quorum 证据 |
| `http-delayed-response` | RoundTripper 延迟 | context deadline、等待取消和资源回收 |
| `http-body-truncated` | response body 中途 EOF | transport fault 与业务空值分离 |
| `http-vendor-extension` | 增加未知字段 | 兼容扩展字段，不污染 canonical key |

故障变换必须是纯函数式的测试装饰器：输入 response，输出新的 response/body，
不修改全局状态，不依赖测试执行顺序。每个变换给响应加上测试内部的 scenario
标识，方便失败报告定位；该标识不进入 EthGuard 的业务 Decision。

## 6. WSS 与长连接场景

WSS 测试分成三层，避免把 payload 故障和帧故障混成一个不透明的代理：

1. **连接生命周期**：Upgrade 拒绝、异常 EOF、半关闭、Close code、重复 Close、
   取消和连接清理；
2. **帧协议**：分片文本、控制帧、非法 opcode、超长 payload、截断长度和写入阻塞；
3. **JSON-RPC payload**：使用 `wstest` 发送缺字段、null、`0x`、vendor envelope
   和错误响应，验证与 HTTP 共用的 admission/resolve 语义。

连接 seam 若接受 `net.Conn`，优先用 `net.Pipe` 驱动半关闭和写阻塞；若 seam 只在
 HTTP Upgrade 后建立，则使用 `httptest.Server` 加 `wstest.Upgrade`。不得为了测试
 `net.Pipe` 强行扩大生产 Transport Port。

WSS 不进入普通 `rpcsample` golden：当前 geth 兼容路径可能把 missing 与 null 压成
相同形态，静态 WSS 记录会把传输实现限制误写成业务兼容契约。WSS 的稳定证据来自
帧/连接 contract 测试；provider 特有 payload 仍由 livecanary 在真实观察时登记。

## 7. 场景模型与断言规则

每个场景包含以下事实：

```text
Scenario {
    Name          // 业务语义名
    Transport     // http / wss
    Source        // Anvil / local peer / external observation
    Method        // eth_call、eth_getBalance 等
    Request       // 原始 RPC 请求
    Mutation      // 延迟、状态码、payload、帧或连接故障
    Expected      // Decision、status、fault、reference、projection
}
```

断言分为三类：

- **协议断言**：请求是否按规范编码，响应是否正确分类，连接是否关闭；
- **业务断言**：Decision status、quorum、reference、canonical value 和 typed
  projection 是否符合现有契约；
- **证据断言**：参与者、fault、presence、来源身份和 provenance 是否完整。

测试不得只断言最终返回值。对于异常场景，至少同时检查 Decision 的状态和故障归属，
避免“结果碰巧相同”掩盖了来源被错误采纳。

## 8. 外部证据与本地回放的关系

`livecanary` 是唯一能够产生 provider-backed 兼容性结论的流程。每份 manifest 至少
包含 provider 标识、chain、请求计划、block/tag、原始响应摘要、时间、客户端版本和
脱敏规则。报告状态必须区分：

- `passed`：不变量和来源校验通过；
- `failed`：观察到可复现的协议或业务偏差；
- `inconclusive`：网络、成员数、权限或样本不足，不能得出结论；
- `not_observed`：本轮没有观察到目标形态，例如真实 `error.data: null`。

`rpcsample/replay` 只负责把已捕获观察重新送入验证路径，保证捕获格式、差异报告和
审计流程稳定。它不会把随时间过期的响应表提升为永久规范，也不会用合成的异常 JSON
关闭 W4/provider-backed golden 门。

本地故障场景可以复现“如果 provider 返回这种形态，客户端如何处理”；它不能写成
“provider 确实返回过这种形态”。Anvil 可以提供真实链状态的基线，但不能替代真实
provider 观察。

## 9. 测试命令与 CI 分层

| 层级 | 默认命令 | 网络 | 失败含义 |
| --- | --- | --- | --- |
| 快速单元/contract | `go test ./... -count=1` | 无 | 代码、协议或不变量回归 |
| WSS/pool 并发 | `go test ./internal/transport/wss ./pool -race -count=10` | 无 | 生命周期、竞争或资源回收回归 |
| Anvil 语义 | `go test ./internal/verification/integration -run '^TestAnvil' -count=1` | fork URL 可能联网 | 本地链语义或 fork 环境不可用；缺少 Anvil时可跳过并报告 |
| 静态检查 | `go vet ./...`、golangci-lint | 无 | 工具和编译期问题 |
| 架构门禁 | `go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1` | 无 | 依赖方向或默认构建污染 |
| Verification 专项 | `go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on` | 无 | 公开调用、证据守恒或 typed 回归 |
| 外部证据 | `go run ./cmd/livecanary ...` | 是 | provider 当前观察；允许 inconclusive，不作为普通 PR gate |

Anvil 测试不默认访问公网。需要 fork URL 时放在显式的本地或定时 workflow；普通 CI
只执行无网络 contract 和行为测试。任何 workflow 若引入公网访问，必须单独标明凭证、
预算、超时、结果状态和失败重跑规则。

## 10. 验收门与停止规则

测试框架实现完成前必须满足：

1. HTTP 与 WSS 至少各有一组合法响应、malformed、延迟/断连和四态 error-data 场景；
2. Anvil 至少覆盖一个固定历史 block、一个 moving tag、一个 EIP-1898 请求和一个
   状态变化后可恢复的场景；
3. 每个异常场景都能指出 fault 属于协议、来源还是业务解析；
4. 本地测试不依赖固定公网端口、不依赖测试执行顺序、不提交秘密和未脱敏 URL；
5. Anvil 生成物有 manifest 和 digest，provider 观察有 livecanary provenance；
6. `go test ./...`、相关 race、`go vet`、架构 interlock 和 Verification 专项门均有
   实际运行结果；
7. 测试通过不能自动关闭 W4、provider compatibility 或真实 null 证据缺口。

遇到以下情况应停止扩展框架并单独评估：需要引入第二套 WSS server、需要在生产代码
中增加测试专用接口、需要把 Anvil 状态转换成大量手写 JSON、需要在默认 CI 中访问
公网，或同一故障无法区分 transport 与 business 归属。

## 10.1 当前证据约束

以下约束是本框架的当前执行规则，不再由阶段性增补文档单独维护：

- 外部 endpoint 只能来自 Chainlist 公共注册表，并通过去凭据过滤；私有、自建或带凭据
  的 endpoint 不能进入 provider-backed 证据。
- Anvil 语义测试使用已抓取、带 manifest 和 digest 的离线 fixture；普通 CI 不因缺少
  fork 网络而静默把跳过算作通过。抓取阶段和执行阶段分开。
- 延迟、取消、WSS 写阻塞和关闭时序优先使用 Go 标准库的 `testing/synctest`；不能用
  真实睡眠作为正确性断言。
- 至少一条 WSS 交叉校验使用独立成熟实现（当前为测试专用的 gorilla/websocket）；
  它不能进入生产依赖或默认构建。
- schema 尚未支持独立的 `not_observed` 顶层状态时，用 `inconclusive` 加
  `state_reason` 表达未观察到的目标形态；不能伪造 provider 证据关闭缺口。
- 原生 Go fuzz 只使用短预算的定时 job，失败语料进入回归；不在普通 PR 关键路径引入
  第三方属性测试框架。

## 11. 与现有文档的关系

- [tooling.md](tooling.md) 规定工具与证据包的依赖方向；本文补充测试层如何使用它们。
- [codebase-topology.md](codebase-topology.md) 继续保持单一
  `internal/verification` Module；本文不新增阶段编号的测试 package。
- [capture-golden-contract.md](capture-golden-contract.md) 继续约束 HTTP capture 和
  WSS golden 边界；本文解释 WSS 为什么由 contract/wstest 覆盖。
- [wss-wire-contract.md](wss-wire-contract.md) 继续拥有 WSS presence、帧和连接语义。
- [project-roadmap.md](project-roadmap.md) 仍是路线状态唯一来源；本文不把测试框架
  提案写入路线完成状态。

本文落库后，测试框架的代码实现应作为独立业务切片推进，并在切片 9–15 完成后重新
审计。未经新的决策，不新增重试、后台重连、provider golden 自动化或第二套执行链。
