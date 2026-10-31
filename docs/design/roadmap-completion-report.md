# Roadmap Completion Report（resolver seam、reference capture、WSS、方法卡片与续轮证据）

状态：**2026-10-31 两段连续执行完成**。前半段记录 resolver seam、reference
capture、WSS W1–W3 与方法卡片；续轮记录 HTTP 事实修正、W4 真实 provider
证据、方法 live probe、freshness/T11 观测和 WSS 生产化决策输入。W4 出口
仍未满足（真实 provider 的 `error.data:null` 未观察），决策 0056 未修订，
WSS 四态 golden 继续禁止。执行明细和问题分类见
[roadmap-execution-ledger.md](roadmap-execution-ledger.md)。

## 1. 提交列表

| 提交 | 意图 |
| --- | --- |
| `d37b4b4` | 建立业务语义执行台账 |
| `dc3c084` | RS5：保留 `rpc.Policy` 为 resolver 深模块（决策 0063 + 契约 §8） |
| `ee702b7` | strict-head malformed 响应在清空前保留原始字节并进入 bounded capture |
| `f6b646d` | finalized reference 的 `Read`/`Confirm` observation 进入 InvocationCapture |
| `6905087` | W1：WSS 调用层可行性评估通过；仅测试原型，无生产 WSS |
| `c643a97` | W2/W3：最小 WSS raw-call Adapter 与进程内契约证据 |
| `7e8b1c4` | WSS abort 关闭 socket，避免读循环泄漏 |
| `b8cc5a0` | WSS 写失败时 context outcome 优先，同时中止不可用连接 |
| `a317e73` | WSS Close 遵守 Transport Port，在途调用由自身 context 控制 |
| `2bf447f` | 23 个 RegisteredMethod 的业务语义对接卡片 |
| `3357f63` | T11 与 reference freshness 的设计不变量（无实现、无默认常数） |
| 本提交 | 台账收尾分类与完成报告 |

没有 push、没有 `reset --hard`、没有 `clean`、没有强制 rebase；所有提交均为
本地提交。

### 续轮提交（2026-10-31）

| 提交 | 意图 |
| --- | --- |
| `1f00a03` | 按代码事实修正 native HTTP 默认、L1 contract 冻结、blobBaseFee/EIP-1898 证据状态 |
| `a173e2a` | W4：opt-in WSS capture/replay 工具与真实 provider 证据（missing/`0x`/value；null 未观察） |
| `fb71884` | 方法证据矩阵与 2026-10-31 四源 live probe |
| `3c2d6ce` | WSS 生产化七个决策输入、freshness 短窗口观测、T11 候选比较 |
| 本提交 | 最终审计、门禁与完成报告更新 |

续轮同样没有 push、`reset --hard`、`clean` 或强制 rebase；W4 capture 目录在
仓库外，未把 endpoint、凭据或 live 结果写成 golden。

## 2. 验证命令与结果

续轮最终门禁（2026-10-31）全部通过：

```text
gofmt -l .                                  # 无输出
go test ./... -count=1                      # 全部通过（42 个有测试的包，11 个无测试；共 53 个包）
go vet ./...                                # 无诊断
go test ./... -race -count=1                # 全部通过
go test ./rpc/... ./rpc/methods/... ./aggregator/... ./internal/runtime/... -race -count=1  # 全部通过
go test ./internal/transport/wss -race -count=10                                           # ok
go test ./internal/transport -run 'TestWSSPrototype' -race -count=5                        # ok
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on  # ok
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1                   # ok
git diff --check                            # 无输出
```

`cmd/wsscapture` 的离线测试（本地 WebSocket server、无外部网络）包含在
`go test ./...` 与 `-race` 中；真实 capture 是手动 opt-in 命令，不进入默认
CI。本报告以续轮门禁为准，前一轮结果保留在对应提交历史中。

## 3. 交付摘要

### 3.1 Resolver seam 收尾

- `docs/decisions.md` 0063：保留 `rpc.Policy` 为 resolver 深模块，不构造
  第二套 ResolverPlan，不为“零 decorator”拆分。
- `resolver-seam-contract.md` §8：触发证据、影响范围、回滚、复核触发条件。
- 未修改 resolver 行为、Decision、quorum、reference、typed 或方法数量。

### 3.2 Reference 原始采集

- strict-head：`jsonsafe.Validate` 前保留原始值，post-admission fault 与
  原始字节配对记录；malformed 响应仍不能赢得聚类。
- finalized：runtime 侧包装 `reference.HeadReader`，记录首轮与确认轮的
  observation 和 collect error；不改 `HeadReader` 签名、reference 算法、
  阈值或成员。
- 回归：`strict_head_raw_capture_test.go`、`finalized_head_raw_capture_test.go`、
  `captured_head_reader_test.go`。

### 3.3 WSS

- W1：复核 go-ethereum v1.17.5 没有取得原始 error envelope 的公开扩展点；
  进程内原型验证 demux、取消、read limit、Close、断连、presence。
- W2：`internal/transport/wss` 实现 raw call、`ChainID`、`Close`；复用
  `rpcwire`；不实现订阅、批量、自动重连、写路径重放或 keepalive。
- Close 只拒绝新调用，等待 pending 各自完成或取消后释放 socket；对端断连、
  读错误和超限仍立即失败 pending，统一现有 `transport.Transport` 契约。
- W3：进程内证据覆盖 missing/null/`0x`/具体 data、malformed frame、
  read limit、cancellation、Close、断连、pending 清理、通知、无自动重试和
  pool seam 集成。
- 架构 interlock：WSS 包不得导入 aggregator、rpc/methods、internal/runtime、
  internal/reference、internal/typed。
- **决策 0056 未修订**，WSS 四态 golden 继续禁止。

### 3.4 方法对接与遗留轨道

- `method-integration-cards.md`：23 个方法的 root、adapter、typed、错误、
  pending、EIP-1898、reference 和证据等级；新增 capture/replay/live 矩阵，
  并记录 2026-10-31 的四源 live probe。
- `adaptive-congestion-control.md`：T11 不变量、信号、状态、验收矩阵、
  候选算法定性比较和 2026-10-31 限流/配额观测；未实现、无默认参数。
- `reference-freshness-policy.md`：两个 freshness 作用点、tag/拒绝/分母
  不变量、待裁决问题和证据计划；2026-10-31 短窗口观测记录在
  `reference-freshness-observations.md`；未实现新策略。
- `docs/design/` 的事实修正：native HTTP 自 2026-10-29 起已实现并成为默认，
  L1 contract 已冻结；blobBaseFee 与 EIP-1898 的旧“unverified/assumed”
  描述已按归档 capture 修正。

### 3.5 HTTP connection seam

- `transport-contract.md` 的 L1 行为已被 native HTTP 和 geth compatibility
  adapter 共同通过（`TestNativeTransportL1Contract`、
  `TestGethTransportL1Contract`）。
- 默认 root facade 安装 native HTTP；`gethcompat` 是唯一显式 compatibility
  seam；`pool` 生产代码不依赖 go-ethereum；默认构建不链接 `ethclient` 或
  `go-ethereum/rpc`。
- 本轮没有重新打开 HTTP contract，也没有进行大爆炸依赖迁移。

### 3.6 W4 真实 provider 证据

- 新增 `cmd/wsscapture`：手动、opt-in 的 WSS capture/replay；不进入默认
  CI，不持久化凭据，不把 live 结果写成 golden。
- 2026-10-31 的 6 个公开无凭据 WSS endpoint × 5 个只读 probe：
  missing=13、`0x`=1、value=10、null=0；24 个错误帧通过 `rpcwire` 和
  `RPCError.DataPresence` 检查；12 个 `eth_call` 错误帧通过真实
  `methods.CallPolicy` 的两源回放。
- 真实 provider 的 `null` 未观察，因此决策 0056 不变，WSS 四态 golden
  继续禁止；详见 `wss-provider-evidence.md`。

### 3.7 WSS 生产化推荐决策矩阵

- `wss-productionization-decisions.md` 已把 config/runtime、adapter 选择、
  reconnect、keepalive、backpressure、health 归属和写路径排除七个问题收敛为
  推荐决策矩阵；每项记录当前事实、选项、语义影响、所需证据、回滚、推荐和
  是否需要主维护者裁决。当前未接入 runtime，未选择任何生命周期参数。
- 矩阵发现两个接线前必须裁决的事实：`SendRawTransaction` 使用同一 pool 的
  固定成员，WSS source 进入该 pool 也会收到写广播；raw WSS 的 presence
  保真会让显式 `null` 产生与 geth WSS `unknown` 不同的 `eth_call` 数据键。
  因此推荐先裁决写路由和 presence 生产行为，再决定是否接线。

### 3.8 方法与 freshness/T11 观测

- 4 源 × 10 请求的 HTTP live probe：latest EIP-1898 object 与
  createAccessList 4/4 成功；hash-position 3 明确拒收 + 1 限流；
  blobBaseFee 2/4 成功；feeHistory 3/4 同一 `oldestBlock` 一致；latest
  block 4/4 同一 26-key 顶层集合；syncing 4/4 为 `false`。
- freshness 4 源 × 3 pass：latest 最大差 1 block，safe 最大差 31，
  finalized 最大差 159；同高度未见 hash 分歧，短窗口未见 reorg。
- T11：观察到一个 endpoint 返回 `-32029 Too Many Requests`，以及
  free-plan/策略拒绝；没有持续容量曲线，无默认参数。

## 4. 剩余风险与分类

完整分类见台账 §11；以下为完成报告视角的摘要。

### 4.1 已解决

- resolver 深模块裁决；
- strict malformed 与 finalized reference 的原始采集；
- HTTP L1 contract 与 native HTTP 默认装配（本轮复核，未新增实现）；
- WSS 调用层可行性、raw-call Adapter 与进程内证据；
- 23 张方法卡片及 capture/replay/live 矩阵；
- W4 capture/replay 工具与真实 provider 的 missing/`0x`/value 证据；
- T11/freshness 设计不变量、观测记录和决策输入。

### 4.2 可接受限制

- WSS 不实现订阅、批量、自动重连、主动 keepalive；这是 W2 的明确非目标。
- WSS 取消发生在已阻塞的写调用时，只有调用 deadline 能中断写；pool 的
  collect timeout 通常提供该 deadline。
- finalized reference 的 participant/selection 诊断仍由 `ReferenceEvidence`
  承载，不新增结构化采集字段。
- WSS Adapter 未接入默认 runtime/config；这是独立配置决策。
- 进程内证据不等于 provider 兼容性结论。
- W4 的 `null` 未在真实 provider 观察到；不能升级四态 golden。
- 方法 live probe 与 freshness 观测都是单次小样本；不是长期兼容性普查。
- T11 只有限流/配额信号，没有持续容量曲线、并发上限或队列增长样本。

WSS `Close` 已遵守 Transport Port：拒绝新调用，等待在途调用完成或取消后
关闭 socket；对端断连和读错误仍立即失败 pending。

### 4.3 需要主维护者决策

- WSS config/runtime、adapter 选择、reconnect、keepalive、backpressure 和
  health 归属（见 `wss-productionization-decisions.md`）；
- T11 算法族、参数与预算范围；
- reference freshness 的 lag 单位、适用范围与自适应；
- 是否以及何时为剩余 provider 证据安排更有针对性的 capture。

### 4.4 外部证据阻塞

- `eth_blobBaseFee` 的多源普查（已有 sample 与 2026-10-31 probe）；
  `eth_syncing` progress object；
- EIP-1898 历史 object 的多源状态可用性（latest 已 4/4）；
- `eth_getBlockByNumber` hash 位置（2026-10-31 无接受者）；
- `eth_createAccessList` 的 hash/canonical 形式；
- `eth_feeHistory` percentile 舍入（2026-10-31 3/4 同一 `oldestBlock`）；
- 完整区块内容承诺（2026-10-31 仅验证顶层 key 集合）；
- WSS 真实 provider 的 `error.data:null`、限流、read limit、响应超时、
  迟到响应与 provider 侧重连（missing/`0x`/value 已观察）；
- T11 容量/限流样本与 freshness 长窗口 reorg/滞后观测。

## 5. 下一阶段入口

1. **W4（剩余证据）**：missing、`0x` 和具体 data 已由真实 WSS frame 证实；
   继续寻找显式 `data:null` 的 provider，或由主维护者接受“null 仍不可知”。
   在证据前不修订 0056、不增加 WSS 四态 golden。
2. **WSS 生产化决策**：`wss-productionization-decisions.md` 的推荐矩阵已
   形成，待主维护者裁决写路径路由、WSS presence 生产行为和 source→adapter
   选择；未裁决前不接入配置/runtime。
3. **逐方法 provider 证据**：latest EIP-1898 object、createAccessList 和
   block 顶层 key 集合已有 live observation；继续补 syncing progress object、
   EIP-1898 历史 object、feeHistory 舍入、完整内容承诺和 hash 位置。
4. **T11**：在受控容量/限流样本基础上选择算法族与参数；当前只有配额/
   策略信号，不写默认参数。
5. **Reference freshness**：先裁决 lag 单位与适用范围；现有短窗口观测
   不修改 `maxLag`、tip gate 或 tag 语义。

## 6. 约束核对

- 每个提交只包含一个结构或实现意图；
- 文件/测试/类型使用业务语义命名，没有 `rs5.go`、`phase_wss_test.go`、
  `task_x_fixture.go`、`stage_*.go`；
- 新行为先补回归测试/设计，再改生产代码；
- 未改变 Decision、quorum、reference、typed、方法数量或写路径语义；
- 未删除历史决策；
- reference 两个缺口分别提交；
- WSS 未混入 resolver、method plan、quorum 或 reference 提交；
- T11/freshness 只补设计、观测和决策输入，未写默认参数或实现；
- W4 capture 是手动、opt-in、仓库外；没有把凭据、未脱敏 endpoint 或 live
  结果写入仓库，也没有把 capture 放进默认 CI；
- 续轮没有改变默认 runtime；HTTP 事实修正只改文档，native 默认与 L1
  contract 已在代码中验证。
