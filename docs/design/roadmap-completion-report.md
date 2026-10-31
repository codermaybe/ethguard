# Roadmap Completion Report（resolver seam、reference capture、WSS 与方法卡片）

状态：**本轮执行完成（2026-10-31）**。本报告汇总本轮从当前 master
继续执行的提交、验证命令、剩余风险和下一阶段入口。执行明细和问题分类见
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
| `2bf447f` | 23 个 RegisteredMethod 的业务语义对接卡片 |
| `3357f63` | T11 与 reference freshness 的设计不变量（无实现、无默认常数） |
| 本提交 | 台账收尾分类与完成报告 |

没有 push、没有 `reset --hard`、没有 `clean`、没有强制 rebase；所有提交均为
本地提交。

## 2. 验证命令与结果

在本报告生成前执行并通过：

```text
gofmt -l .                                  # 无输出
go test ./... -count=1                      # 全部通过（41 个有测试的包）
go vet ./...                                # 无诊断
go test ./... -race -count=1                # 全部通过
go test ./rpc/... ./rpc/methods/... ./aggregator/... ./internal/runtime/... -race -count=1
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
git diff --check                            # 无输出
```

WSS 专项重复验证：

```text
go test ./internal/transport/wss -race -count=10
go test ./internal/transport -run 'TestWSSPrototype' -race -count=5
```

## 3. 本轮交付

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
- W3：进程内证据覆盖 missing/null/`0x`/具体 data、malformed frame、
  read limit、cancellation、Close、断连、pending 清理、通知、无自动重试和
  pool seam 集成。
- 架构 interlock：WSS 包不得导入 aggregator、rpc/methods、internal/runtime、
  internal/reference、internal/typed。
- **决策 0056 未修订**，WSS 四态 golden 继续禁止。

### 3.4 方法对接与遗留轨道

- `method-integration-cards.md`：23 个方法的 root、adapter、typed、错误、
  pending、EIP-1898、reference 和证据等级；provider 缺口显式登记。
- `adaptive-congestion-control.md`：T11 不变量、信号、状态、验收矩阵和
  未决参数；未实现。
- `reference-freshness-policy.md`：两个 freshness 作用点、tag/拒绝/分母
  不变量、待裁决问题和证据计划；未实现新策略。

## 4. 剩余风险与分类

完整分类见台账 §11；以下为完成报告视角的摘要。

### 4.1 已解决

- resolver 深模块裁决；
- strict malformed 与 finalized reference 的原始采集；
- WSS 调用层可行性、raw-call Adapter 与进程内证据；
- 23 张方法卡片；
- T11/freshness 设计不变量。

### 4.2 可接受限制

- WSS 不实现订阅、批量、自动重连、主动 keepalive；这是 W2 的明确非目标。
- WSS 取消发生在已阻塞的写调用时，只有调用 deadline 能中断写；pool 的
  collect timeout 通常提供该 deadline。
- finalized reference 的 participant/selection 诊断仍由 `ReferenceEvidence`
  承载，不新增结构化采集字段。
- WSS Adapter 未接入默认 runtime/config；这是独立配置决策。
- 进程内证据不等于 provider 兼容性结论。

### 4.3 需要主维护者决策

- WSS Close 的 in-flight 语义与 `transport.go` 通用注释的偏差；
- WSS reconnect/reorg/backpressure 与 keepalive 契约；
- 是否以及何时把 WSS 接入配置/runtime；
- T11 算法族、参数与预算范围；
- reference freshness 的 lag 单位、适用范围与自适应；
- 真实 provider capture 的排期。

### 4.4 外部证据阻塞

- `eth_blobBaseFee`、`eth_syncing` progress object；
- EIP-1898 object 形状；
- `eth_getBlockByNumber` hash 位置；
- `eth_createAccessList` 兼容性；
- `eth_feeHistory` percentile 舍入；
- 完整区块内容承诺；
- WSS 真实 provider 的 error envelope、限流、消息大小与重连；
- T11/freshness 的 provider 观测。

## 5. 下一阶段入口

1. **W4（外部证据）**：采集真实 WSS provider 的 error.data presence、
   read limit、限流与断连行为；只有进程内与真实 provider 证据都通过，才
   修订决策 0056 或增加 WSS 四态 golden。
2. **WSS 生产化决策**：决定 Close 语义、reconnect/backpressure/keepalive
   和配置接线；在此之前 WSS 只作为可替换 Adapter 存在。
3. **逐方法 provider 证据**：按 `method-integration-cards.md` §5 的缺口
   顺序采集 capture/replay/live；不用合成样本关闭缺口。
4. **T11**：在真实容量/限流样本基础上选择算法族与参数；保持成员、分母、
   权重、模式和门槛不变。
5. **Reference freshness**：先裁决 lag 单位与适用范围，再实现；
   保持 tag 语义和“freshness 失败=拒绝”不变量。

## 6. 约束核对

- 每个提交只包含一个结构或实现意图；
- 文件/测试/类型使用业务语义命名，没有 `rs5.go`、`phase_wss_test.go`、
  `task_x_fixture.go`；
- 新行为先补回归测试/设计，再改生产代码；
- 未改变 Decision、quorum、reference、typed、方法数量或写路径语义；
- 未删除历史决策；
- reference 两个缺口分别提交；
- WSS 未混入 resolver、method plan、quorum 或 reference 提交；
- T11/freshness 只补设计与不变量，未写实现。
