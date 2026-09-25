# T06 后续执行决策地图

这是 T06 实施阶段的紧凑任务图。`project-roadmap.md` 记录项目计划和出口；本文件记录本轮执行任务之间的依赖，供执行者逐项领取、供审计者逐项验收。

规则：任务只提交其范围内的代码、测试、样本或文档；不得顺手引入 `MethodContract` DSL、代码生成、统一测试 runner 或未批准的裁决语义。每个任务必须提交验收证据后才能解除后续任务的阻塞。

## #1: 冻结 T06 设计不变量

Blocked by: none

Type: Discuss

### Question

Decision 0039 的方案 (i)、删减式投影、维度证据、原始 JSON-RPC error 回放和“无测试 DSL”边界，是否已经在标准、roadmap、架构记录和方法契约中一致？

### Answer

已选方案 (i)，并于 2026-09-25 写入 `method-verification-pipeline.md` 第 1.1 节。`Decision.Value` 允许方法声明的删减式投影，投影只能删除未佐证字段，不能新增、替换或跨来源拼装。`rpc.RPCError` 已有 `Data`；实际缺口是 `rpctest` 原样 error-body 和 `observationRPCError` 对缺失 / `null` presence 的保留。0039 实现仍属于 #6，不得提前开始。

验收证据：第 1.1 节列出目标语义与当前实现事实；架构记录、决策 0039、方法契约 0.5 与 `rpc.Decision` 注释指向同一边界。样本 / golden 无变化。审计接受前 #2 / #3 保持阻塞。

## #2: 建立精确的 matrix 测试互锁

Blocked by: #1

Type: Prototype

### Question

如何在不把 Markdown 变成测试 DSL 的情况下，确认 matrix 的 `Tests` 列引用真实测试，并保持 RegisteredMethod 双向覆盖？

### Answer

只解析 `moving-tag-matrix.md` 的 `Tests` 列，只接受完整 `func Test*` 名称。清理 `Prefix*` 和 `as above`；method-contracts 与 roadmap 中的测试名只作说明。保留现有列数、非空单元格、catalog 双向覆盖和 forbidden-claims 检查。

验收证据：新增静态检查测试；matrix 中不再有通配或别名；故意删除或改名一个测试时检查能失败；恢复后 `go test ./rpc/methods/...` 通过。

## #3: 固定 anchor transport seam

Blocked by: #1

Type: Discuss

### Question

WSS anchor 如何接入而不产生第二套参考、quorum、状态或证据语义？

### Answer

HTTP 和 WSS 只负责提供 reference observations；参考选择、门槛、`ReferenceEvidence` 和失败语义共用现有 anchor resolver。先记录 transport-neutral 的请求与证据形状，再实现 WSS。T06.2 / T06.3 依赖该语义稳定，但 #2 可以并行。

验收证据：有一份 seam 设计和最小进程内测试；HTTP/WSS 的状态和证据字段映射相同；无第二套 anchor cache 或 quorum 规则。

## #4: 建立真实 replay seam

Blocked by: #1, #3

Type: Prototype

### Question

如何让同一批样本依次通过 Policy、Admission、Aggregator 和 typed-read gate，并保留错误响应的原始 data presence？

### Answer

提取 `internal/rpcsample/replay`；增加 `rpctest` 原样 JSON-RPC error-body 能力，至少区分缺失 data、`null`、`0x` 和具体 data；为 `latest` 提供进程内 anchor 应答。Policy 层和端到端层都必须使用真实生产路径，禁止外部网络。

验收证据：同一 fixture 同时通过 Policy replay 和 `Aggregator.Aggregate`；端到端测试覆盖 Admit 与 typed gate；记录 sha256 校验失败时测试失败；`go test ./...` 通过。

## #5: 固定 golden schema 与批次归档

Blocked by: #4

Type: Prototype

### Question

如何让分类、维度证据和删减式投影的变化在 review 中可见，同时保持样本防篡改凭据？

### Answer

每个批次保留 `index.json` 和 record sha256，新增版本化 `expect.json`。Golden 至少记录 mode、floor、N、每源比较键、准入 Faults、维度级 Support/Reject/Abstain、删减字段和最终 Decision/error。文件是数据，不包含条件、继承、通配或生成逻辑。

验收证据：现有样本可迁移且 record 内容和 sha256 不变；CI 能重放并比较 golden；篡改 record、配置或分类逻辑时产生可审阅失败或 diff。

## #6: 落地 0039 规则 4e

Blocked by: #1, #4, #5

Type: Prototype

### Question

如何在不改变 generic Strict 单 key 语义的情况下，为 `eth_call` 实现行为维度与 revert-data 维度的独立裁决？

### Answer

使用方法专属多维 resolver。revert 行为达到门槛即可保持 `Corroborated`；具体 data 达不到门槛时采用删减式投影，typed `CallContract` 返回无 data 的 corroborated revert，原始字节只经 Uncertain / 审计证据可见。弃权源留在分母，不进 data Reject，不用其他来源补全。

验收证据：覆盖 N=2、N=3、N=4、全 abstain、data 达到 quorum、具体 data 冲突、unanimity；验证 `Decision.Value`、`RevertError.Data`、Uncertain 和 typed gate；更新 method-contracts、matrix、golden；`go test ./...` 通过。

## #7: 逐方法扩展读取批次

Blocked by: #2, #3, #4, #5, #6

Type: Discuss

### Question

如何增加 RegisteredMethod 而不重新复制测试、文档和事实来源？

### Answer

采用 tracer-bullet vertical slice：每次只增加一个方法或一组同语义方法，同时提交 matrix、Policy、admission / normalization、pending / provider error、端到端 replay、golden 和标准第 5 节核验记录。复用普通 Go helper 和 table-driven tests，不增加声明式契约语言。

验收证据：新增方法通过标准清单；matrix 与 catalog 双向检查通过；Policy 与 Aggregator 两层 replay 通过；golden 和样本状态已提交；审计记录说明它提供 provider-backed evidence，不宣称 Ethereum consensus proof。
