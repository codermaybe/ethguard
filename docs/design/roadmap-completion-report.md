# Roadmap Completion Report（resolver seam、reference capture、WSS、方法卡片与续轮证据）

状态：**2026-11-01 统一 Source 已实施**。显式 WSS 与 HTTP 共用固定读写 pool，
通用限流 health 分类已修复；写结果/未知 delivery、生命周期、固定 basis 与四态
presence 已有确定性行为覆盖和公开入口 smoke。0056.2/0064 批准并记录生产范围。
W4/provider-backed 四态 golden 门仍未满足：真实 null 未观察；T11 与 freshness
新策略未实现。下文 2026-10-31 的提交与阶段结果保留为历史，当前交付见 §7，
不以旧阶段的“未接线/未修订”覆盖新决策。执行明细见
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

### 本轮续执行（2026-10-31，六阶段）

| 提交 | 意图 |
| --- | --- |
| `3b5b135` | WSS 生产化七个问题收敛为推荐决策矩阵（未接线） |
| `06ef137` | WSS capture 增加 block-hash-position probe；8×6 复测仍无 `null` |
| `95145a0` | 历史块 18,000,000 的 EIP-1898/createAccessList/block/feeHistory 证据 |
| 本提交 | freshness 扩展窗口、分类与最终门禁 |

本轮没有 push、`reset --hard`、`clean` 或强制 rebase；没有修改 WSS adapter、
默认 runtime、pool、aggregator、reference、typed、quorum 或写路径。

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

本轮六阶段收尾再次运行同一门禁集合并全部通过；WSS `-race -count=10`、
verification `-race -count=2 -shuffle=on` 和 architecture interlock 均通过。
真实 provider probe 仍是手动、仓库外、只读的 live observation，不进入默认
CI，也不写入 golden。

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
- `gorilla/websocket` 当时记录为 raw WSS adapter 的临时直接依赖（历史记录）；
  该承诺已于 2026-11-03 兑现：自有 wire 栈（标准库握手/帧/连接层）替换
  gorilla，直接依赖删除，仅存 go-ethereum 兼容通道的 indirect 残留；见
  [wss-wire-contract.md](wss-wire-contract.md) 与执行台账 §17。
- 2026-10-31 的 6 个公开无凭据 WSS endpoint × 5 个只读 probe：
  missing=13、`0x`=1、value=10、null=0；24 个错误帧通过 `rpcwire` 和
  `RPCError.DataPresence` 检查；12 个 `eth_call` 错误帧通过真实
  `methods.CallPolicy` 的两源回放。
- 同日复测 8 个 WSS endpoint × 6 个 probe（新增 block-hash-position）：
  48 条记录、2 个拨号失败、6 个可用 endpoint 的 30 条协议错误为
  missing=16、`0x`=1、value=13、null=0；未重复 `1006`。`null` 仍未观察，
  因此决策 0056 不变，WSS 四态 golden 继续禁止；详见
  `wss-provider-evidence.md`。

### 3.7 历史决策输入（2026-10-31；已由 0056.2/0064 解决）

- `wss-productionization-decisions.md` 已把 config/runtime、adapter 选择、
  reconnect、keepalive、backpressure、health 归属和写路径排除七个问题收敛为
  推荐决策矩阵；每项记录当前事实、选项、语义影响、所需证据、回滚、推荐和
  是否需要主维护者裁决。当前未接入 runtime，未选择任何生命周期参数。
- 矩阵发现两个接线前必须裁决的事实：`SendRawTransaction` 使用同一 pool 的
  固定成员，WSS source 进入该 pool 也会收到写广播；raw WSS 的 presence
  保真会让显式 `null` 产生与 geth WSS `unknown` 不同的 `eth_call` 数据键。
  因此推荐先裁决写路由和 presence 生产行为，再决定是否接线。
- 本轮没有启动最小 WSS 接线：没有主维护者决策记录，且写路由/presence
  选择仍未裁决；Phase 3 保持阻塞，不写成已完成。

### 3.8 方法与 freshness/T11 观测

- 4 源 × 10 请求的 HTTP live probe：latest EIP-1898 object 与
  createAccessList 4/4 成功；hash-position 3 明确拒收 + 1 限流；
  blobBaseFee 2/4 成功；feeHistory 3/4 同一 `oldestBlock` 一致；latest
  block 4/4 同一 26-key 顶层集合；syncing 4/4 为 `false`。
- 同日定向历史块 probe（4 源 × 11 请求，block 18,000,000）：balance 的
  blockNumber/blockHash object 4/4；nonce/storage/code/createAccessList 的
  blockNumber object 4/4；createAccessList 的 blockHash object
  （requireCanonical false/true）4/4；feeHistory 固定区间
  `[1,33,67,99]` 4/4 返回相同 reward；历史 block header/full 4/4（22 个
  顶层字段、94 笔交易、16 withdrawals、0 uncles）；full 交易对象有 1 个源
  省略 `blockTimestamp`；hash-position 4/4 拒收；syncing 4/4 `false`。
- freshness 短窗口 4 源 × 3 pass：latest 最大差 1 block，safe 最大差 31，
  finalized 最大差 159；扩展窗口 4 源 × 8 pass、约 45 秒间隔、约 6 分 22 秒：
  latest 最大 spread 1，safe 224，finalized 96；一个源的 safe/finalized 在
  全部 8 pass 固定不动而 latest 仅落后 1 block；未观察到回退、同高度 hash
  冲突或 reorg；一次 HTTP 503 与 freshness 分开归因。
- T11：观察到一个 endpoint 返回 `-32029 Too Many Requests`，以及
  free-plan/策略拒绝；没有持续容量曲线，无默认参数。

## 4. 剩余风险与分类

完整分类见台账 §11；以下为完成报告视角的摘要。

### 4.1 已解决

- resolver 深模块裁决；
- strict malformed 与 finalized reference 的原始采集；
- HTTP L1 contract 与 native HTTP 默认装配（本轮复核，未新增实现）；
- WSS 调用层可行性、raw-call Adapter 与进程内证据；
- 显式 WSS/HTTP per-source 路由与统一读写 Source、common rate-limit health、
  未知写结果不重放、固定 basis/cooldown、生命周期与 raw 四态生产保真（§7）；
- 23 张方法卡片及 capture/replay/live 矩阵；
- W4 capture/replay 工具与真实 provider 的 missing/`0x`/value 证据；
- T11/freshness 设计不变量、观测记录和决策输入。

### 4.2 可接受限制

- WSS 不实现订阅、批量、自动重连、主动 keepalive；这是 W2 的明确非目标。
- WSS 取消发生在已阻塞的写调用时，只有调用 deadline 能中断写；pool 的
  collect timeout 通常提供该 deadline。
- finalized reference 的 participant/selection 诊断仍由 `ReferenceEvidence`
  承载，不新增结构化采集字段。
- reference freshness 只有约 6 分 22 秒的扩展窗口观测；未观察到 reorg，
  但不足以证明 reorg 分布，也不产生默认 lag。
- WSS 只在显式 `transport: wss` 时启用；缺省仍 native HTTP，不自动 fallback。
- 进程内证据不等于 provider 兼容性结论。
- W4 的 `null` 未在真实 provider 观察到；不能升级四态 golden。
- 方法 live probe 与 freshness 观测都是小样本 live observation（方法为
  4 源，freshness 为短窗口与约 6 分钟窗口）；不是长期兼容性普查。
- T11 只有限流/配额信号，没有持续容量曲线、并发上限或队列增长样本。

WSS `Close` 已遵守 Transport Port：拒绝新调用，等待在途调用完成或取消后
关闭 socket；对端断连和读错误仍立即失败 pending。

### 4.3 需要主维护者决策

- 未来 WSS 后台 reconnect、主动 keepalive、queue 等扩展需另立决策；本期已明确
  不启用。config/runtime、写路由及 raw presence 生产范围已由 0056.2/0064 解决。
- T11 算法族、参数与预算范围；
- reference freshness 的 lag 单位、样本过期语义、显式历史读取与 tip gate
  的关系，以及是否自适应；
- 是否以及何时为剩余 provider 证据安排更有针对性的 capture。

### 4.4 外部证据阻塞

- `eth_blobBaseFee` 的多源普查（已有 sample 与 2026-10-31 probe）；
  `eth_syncing` progress object；
- EIP-1898 其他历史高度/archive 策略（block 18,000,000 已 4/4）；
- `eth_getBlockByNumber` hash 位置（2026-10-31 4/4 拒收）；
- `eth_createAccessList` 的 hash/canonical 形式：block 18,000,000 的
  blockHash object（requireCanonical false/true）4/4，但仍单块；
- `eth_feeHistory` 多区间 percentile 舍入（固定区间 `[1,33,67,99]` 4/4 一致）；
- 完整区块内容承诺：block 18,000,000 的 header/full 4/4、16 withdrawals、
  0 uncles；uncle 样本与 full 交易对象字段差异；
- WSS 真实 provider 的 `error.data:null`、限流、read limit、响应超时、
  迟到响应与 provider 侧重连（missing/`0x`/value 已观察）；
- T11 容量/限流样本与 freshness 更长窗口 reorg/滞后观测（目前约 6 分 22 秒，
  不足以证明 reorg 分布）。

## 5. 下一阶段入口

1. **W4（剩余证据）**：真实 missing/`0x`/value 已观察；2026-11-01 的 3×6
   补采仍无 null。继续收集自然出现的真实 null、限流、read limit、迟到响应和
   断连；不制造负载，不以合成 capture 升级 provider-backed golden。
2. **WSS 生产接线已完成**：显式 per-source transport、统一读写 pool 与 raw
   presence 保真按 0056.2/0064 落地。后续关注部署方法能力与连接故障分布，
   不因 transport 类型新增只读角色或动态缩小 basis。
3. **逐方法 provider 证据**：latest EIP-1898 object、createAccessList、
   历史 block 18,000,000 的 object 形式、feeHistory 固定区间和 block
   header/full 已有 live observation；继续补 syncing progress object、
   EIP-1898 其他历史高度、feeHistory 多区间舍入、uncle 内容、full 交易
   字段差异和 hash 位置。
4. **T11**：在受控容量/限流样本基础上选择算法族与参数；当前只有配额/
   策略信号，不写默认参数。
5. **Reference freshness**：先裁决 lag 单位、样本过期语义、显式历史读取与
   source-level tip gate 的关系；两次观测（短窗口与约 6 分钟扩展窗口）只
   证明 provider tag 节奏差异，不修改 `maxLag`、tip gate 或 tag 语义。

## 6. 历史约束核对（2026-10-31；当前实施见 §7）

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

## 7. 统一 Source 实施交付（2026-11-01）

### 生产行为

- `pool/collect.go` 在 protocol error 判定前使用 `isRateLimitError`，code-only
  `429`/`-32005`/`-32016` 记 failure、限流计数并降速；`-32601` 保持健康并清零
  连续失败。HTTP/WSS 共用该规则，不新增 WSS health policy。
- `config.Upstream.Transport` 显式选择 `native-http` / `wss`；组合根将 Dialer
  固定绑定到既有 `pool.Member`/`Source`，nil 继承默认。配置检查 scheme 匹配，
  显式 transport 与注入 dialer 冲突时拒绝。业务方法不读 scheme，无第二 registry。
- `SendRawTransaction` 保持既有 Broadcast/Forward，无 WSS 专用写分支；固定成员、
  basis、门槛、单位权重不变。unsupported 不支持结果，但不会退出读 health。
- WSS 等待写锁后检查 context，已取消且尚未写 frame 的调用不发送、不 abort。
  frame write failure/read failure/read limit 才终止失效连接及相关 pending；
  Close 拒绝新调用，等待已有 pending 响应或自身 context 结束。
- read limit 同时保留 body-too-large 与 connection-closed，pool 立即丢弃坏连接。
  ChainID admission 阶段发生连接失败也按同一规则回收，避免下一调用再次撞上坏 socket。
- 未知交易不重放；`WriteRejected` 可以包含 silent 的未知 delivery，不能解释成
  交易无效或未提交。同一 backend 的 HTTP/WSS endpoint 不是两个独立信任源。

### 行为证据与检查

- 共用限流 smoke：三种 code 的 failure/rateLimited 各累计 3 次；中间 `-32601`
  健康响应清零连续失败，pace 从 40ms 经 80ms 到 160ms。
- 公开构造入口的混合源 smoke：首次两源 accepted；WSS 收到签名 frame 后断连时
  disputed，basis=2、required=2、support=1、faults=1，发送数未增加；下一次独立
  调用建立第二条 socket 后 accepted。混合 null revert 的 data 维度两源均弃权，
  返回 `{}`，不把 null 当具体 bytes。
- admission 断连 smoke：首次 `eth_blockNumber` 在 ChainID 阶段失败，未重放；
  下一次独立调用重拨第二条 socket，返回 Forwarded `"0x1"`，health 为一次
  failure、一次 success。read-limit admission 回归已观察修复前失败、修复后通过。
- `pool/rate_limit_health_test.go` 覆盖两种 transport 的 code-only 限流、server
  error、unsupported reset、三次失败 cooldown、读写 gate、固定成员及恢复。
- `pool/write_outcome_health_test.go` 覆盖一次断连导致两个 attempted emission
  分别记失败、无 invocation replay、独立重拨，以及业务/ChainID admission 两个
  阶段 read-limit 后直接重拨。
- `unified_source_write_test.go` 覆盖 mixed accepted/disputed/rejected+silent、写
  health 分层、实际签名 bytes 与单 WSS forwarded basis；
  `unified_source_presence_test.go` 覆盖真实 loopback wire 四态、比较关系、data
  支持/弃权/删除边界。`internal/transport/wss/lifecycle_test.go` 覆盖各生命周期。
- capture/replay 保留离线 presence、scenario 参数和本地采集回放测试；这些测试只
  验证工具和业务解码边界，不把 CLI 文案当协议合同，也不生成 provider golden。

执行检查集合：`gofmt -l .`、`go test ./... -count=1`、`go vet ./...`、
`go test ./... -race -count=1`、`go test ./internal/transport/wss -race -count=10`、
`go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on`、
`go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1`、
`git diff --check`。2026-11-01 最终完整序列全部通过：format/diff 无输出，vet
通过；全量普通与 race 各 42 个测试包通过、11 个无测试包；WSS race×10、
Verification race×2 shuffled、architecture/package/default-build 检查全部通过。

### 外部证据边界

真实 provider 补采与 replay：3 个公开 endpoint × 6 个只读 scenario，3 次 chain
check、6 次 call-policy check，15 个错误帧 missing=10、value=5、null=0、empty-hex=0。
每个 scenario 主请求后有一次同连接 `eth_chainId` follow-up：15 次成功，p02 在
unknown-method、invalid-params、block-hash-position 后 3 次遇到 `1006 unexpected EOF`，
证据在各 record 的 `follow_up.close_code`。未观察到限流、read limit、迟到响应或
响应超时。capture 位于仓库外 `/tmp/ethguard-wss-unified-evidence-20261101`；
本轮未重复 `0x`，先前观察仍有效。断连证据不代表生产 adapter 重连已经外部验证。
没有公共网络写交易，也不把进程内 frame failure、限流或 null 测试当外部证据。
W4、T11、freshness 不因这次生产接线或小样本补采标为完成。
