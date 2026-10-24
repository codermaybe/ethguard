# T09：场景矩阵与字段生命周期不变量

状态：**设计完成，待实现**（2026-10-24）  
维护方式：本文件是 T09 的自包含执行契约。新的 agent 只需读取本文件、代码和
引用的冻结规范即可恢复进度；`project-roadmap.md` 只维护摘要、提交号和最终状态。

## 1. 目标

T09 为所有已注册的 Ethereum Execution RPC 方法补齐两类离线验证：

1. **字段生命周期不变量（T09.2）**：证明受保护字段从原始响应经过准入、规范化、
   比较键、规范编码、`Decision.Value` 和 typed projection 时没有无记录地消失、
   被替换或由其他来源拼接。
2. **决策场景矩阵（T09.1）**：证明每个方法在固定分母和固定门槛下，对同意、
   少数异见、离群、单源故障、全源故障、冲突、notfound、pending/local 等场景
   产生符合方法契约的状态和证据。

T09 是离线 regression 里程碑。它验证实现内部的语义闭包，不验证 provider 的
真实行为，也不把合成结果写成外部可信证据。

## 2. 完成定义

T09 只有在 T09.2、T09.1 和最终审计全部通过后才算完成。完成时必须满足：

- `RegisteredNames()` 中的全部方法均有字段生命周期覆盖和场景矩阵行；新增注册
  方法若缺少任一覆盖，CI 必须失败。
- 测试沿生产路径通过 Policy、Aggregator 和现有 typed adapter；不得只直接调用
  一个 policy 函数来伪造端到端结果。
- 固定分母、门槛、代表源、Support/Reject/Abstain、Faults、状态和 Value 均有
  可审阅断言；准入或编码失败不得缩小分母或降低门槛。
- 变形、变异、往返和字段守恒四类不变量均有回归覆盖；不变量失败时错误指向
  方法和字段，而不是只报告一个总计数。
- 全部测试离线、可重复，且 `go test ./... -count=1`、`go vet ./...` 通过。
- 没有修改历史 capture、`expect/v1`、`batch.json`、reference policy、WSS
  transport、方法目录、生产裁决语义或 ethclient transport。
- 没有新增 provider 兼容性、canonicality、consensus 或 finality 声明。

## 3. 冻结边界

以下规则在 T09 中不可重新讨论：

- 受保护结果、规范编码、`EncodeCheck`、`EncodeCommit`、字段进入依据和五类
  报文差异沿用 [`return-value-design.md`](return-value-design.md) 与
  [`method-verification-pipeline.md`](method-verification-pipeline.md) 的现行口径。
- Policy、Aggregator、admission index、现有 encoder 和 typed adapter 是行为事实
  来源。不得引入 `MethodContract` DSL、代码生成、通用 `suite.RunAll`，也不得
  建立第二份运行时字段注册表。
- `Decision.Value` 只能来自该方法已经定义的受保护结果域；不能从比较键反序列化，
  不能把多个来源字段拼成新对象，不能用另一来源补齐缺失字段。
- EncodeCheck/Admit/typed decode 失败保留在固定分母中并形成 Fault；可弃权只适用
  于方法明确声明的字段。缺失、`null`、`0x` 和合法 notfound 不得混为一谈。
- `WSS` 对 missing/null 的限制继续按决策 0056 记录；T09 不修传输 seam。
- 合成测试不得触网，不写入 `internal/rpcsample/testdata`，不新增 golden，不能
  把本地场景当 provider capture。

T10 已经完成并提供外部 live 证据。T09 不重做 T10，也不把 T10 的
`inconclusive` observation 改成 passed。ethclient/transport 迁移另立架构任务，
不得混入 T09。

## 4. 方法全集

本次 T09 的覆盖全集由 `rpc/methods.RegisteredNames()` 决定，当前固定为 23 个：

```text
eth_chainId
eth_getBlockByHash
eth_getBlockByNumber
eth_getBalance
eth_getTransactionReceipt
eth_getTransactionByHash
eth_getBlockReceipts
eth_getTransactionCount
eth_getStorageAt
eth_getCode
eth_call
eth_estimateGas
eth_getLogs
eth_feeHistory
eth_gasPrice
eth_maxPriorityFeePerGas
eth_blobBaseFee
eth_blockNumber
eth_createAccessList
net_peerCount
eth_syncing
net_version
net_listening
```

`eth_sendRawTransaction` 属于 WriteMethod，不进入本次 read registry 矩阵；
`eth_getProof` 仍是 UnsupportedMethod，也不进入 T09。若目录发生变化，测试必须
先失败，agent 不能静默扩大或缩小覆盖范围。

## 5. 执行顺序和提交边界

执行顺序固定为：**T09.2 → T09.1 → T09.3 最终收口**。每个实现切片保持独立
提交，agent 不等待中途维护者确认；完成全部切片后再由维护者进行一次整体审计。

### T09.2：字段生命周期不变量

**目的**：锁住“一个字段从 raw response 到公开 typed result 的处置”。

**实施步骤**：

1. 从现有 admission、encoder、normalizer、method rule 和 typed adapter 读取每个
   方法的字段进入依据；不复制成运行时注册表。测试中的字段断言必须靠方法已有
   encoder/helper 表达，必要的说明写在测试附近。
2. 为每个方法补齐四类性质：表示变形、受保护字段变异、往返、字段守恒。
3. 将每种变体放到不同 pool 位置，验证 Value 不随捕获顺序变化；代表源仍按既有
   捕获顺序选择，不能把代表源当成第二个投票结果。
4. 对必填字段删除、受保护字段篡改、错误请求绑定和坏 typed payload，断言在
   Admit 或 EncodeCheck 失败，来源仍在分母，不能退回其他来源的字段。
5. 对通过检查的 payload，验证 `typed(raw)` 与 `typed(Encode(raw))` 等价；派生
   字段按公开派生规则重新计算，不从另一来源复制。
6. 对每份 observation 检查字段最终落入 `Value`、`Extensions`、`Diagnostics`、
   合法删减式投影，或有明确的 admission/encode fault；任何未分类消失都失败。

**四类性质的最低断言**：

| 性质 | 必须覆盖 | 失败含义 |
| --- | --- | --- |
| 表示变形 | JSON 键序、空白、允许的 hex 大小写、`0X`、JSON 字符串转义、厂商附加字段重排 | 规范化或编码不稳定 |
| 受保护字段变异 | 删除、篡改、错误绑定、非法类型、超范围值 | Admit/EncodeCheck 放行了不完整结果，或错误缩小了分母 |
| 往返 | raw → typed 与 raw → Encode → typed；对象、数组、标量分别覆盖 | 编码器改变语义，或 typed 路径绕过 Value |
| 守恒 | 受保护、派生、域外、诊断、删减、fault 的去向 | 字段静默消失、跨来源拼接或未经声明进入 Value |

没有独立 typed adapter 的方法，不得新造 adapter；使用现有生产 projection/Value
路径并在测试中明确其适用边界。若该方法无法完成可审阅的 typed 或 Value 断言，
记录为硬阻塞，不能用“暂不适用”掩盖缺口。

**T09.2 交付物**：实现测试、覆盖互锁、必要的测试辅助函数、提交说明和测试日志。
不得提交 capture、expect、golden 或 live artifact。

### T09.1：决策场景矩阵

**目的**：锁住每个注册方法在固定分母下的裁决和证据形状。

使用现有 `rpctest.Node`、Policy seam、Aggregator 和 typed adapter 构造合成
来源。共享辅助函数可以复用 transport/observation 构造，但每个方法必须有显式
可定位的矩阵行和方法测试；不能用一个隐藏所有方法的通用 runner 或代码生成器。

每个方法至少覆盖下列八种场景：

| 场景 | 构造 | 必查结果 |
| --- | --- | --- |
| `all-agree` | 全部分母返回同一合法值 | 正常成功状态、全部支持、Value/typed 一致 |
| `minority-dissent` | 达到门槛的多数值与少数合法异值 | 多数状态、Support/Reject、代表源和 Value |
| `outlier` | 一个异值，其余来源相同 | 异值不得进入 Value，不得误判为 fault |
| `single-fault` | 一个来源 transport/admission/encode fault | Fault 保留，分母不变，门槛不下降 |
| `all-fault` | 所有来源失败或不合格 | `Unknown`/方法允许的失败状态，无成功 Value |
| `conflict` | 两个或多个合法值无法达到门槛 | `Unknown`/`ErrConflict`，保留支持与反对证据 |
| `notfound` | 方法合法的 `null`/空结果/receipt notfound | 只按方法契约返回，不与 malformed 混同 |
| `pending/local` | 方法支持或拒绝的 pending、节点本地状态 | 按 matrix 契约状态；不把本地观测升格为 corroborated |

每个矩阵行必须同时断言：

- `Decision.Status`、错误类别和方法允许的状态集合；
- 固定 `N`、floor、Support、Reject、各维度 Abstain、Faults；
- representative 与 Value 的来源/规范编码关系；
- Aggregator 产出的公开结果及 typed decode；
- anchor、moving-tag、pending 和错误路径是否走了正确的生产 seam。

矩阵不得因为某方法没有某种语义而删掉场景。对不适用场景必须断言契约规定的
本地拒绝、notfound 或其他明确结果，并写出原因。不能把所有方法强行套同一个
`Corroborated`/`Unknown` 期望。

**T09.1 交付物**：覆盖全部注册方法的显式矩阵测试、注册表↔矩阵互锁、失败路径
测试、提交说明和测试日志。仍不得修改 provider capture 或 golden。

### T09.3：最终收口和整体审计

T09.3 不新增生产语义，只收集 T09.2/T09.1 的结果并更新文档：

1. 在本文件的进度表填写两个提交号、覆盖方法数、测试命令和未解决风险。
2. 在 `project-roadmap.md` 将 T09 标记为完成，保留 T10 已完成的独立证据边界。
3. 运行最终审计命令并保存结果；不把本地测试输出复制成 golden 或外部证据。
4. 若发现失败来自真实 provider、WSS 或 ethclient 行为，记录为后续任务，不在
   T09 中偷偷扩大范围。

## 6. 静态互锁和防回退

T09 必须增加或复用以下互锁：

- `RegisteredNames()` 与 T09.2 覆盖、T09.1 矩阵双向一致；注册方法缺测试、
  测试命名不存在或矩阵行重复时失败。
- 新增字段进入现有 encoder/schema/admission 后，若没有对应生命周期断言，CI 失败；
  互锁应检查测试声明/方法覆盖，不建立第二份运行时字段表。
- 场景测试使用固定分母和固定 floor；任何测试通过删除故障来源来提高支持率，均应
  失败。
- `go test ./... -count=1` 不得触网。若测试意外创建外部 HTTP/WSS 请求，应作为
  T09 失败处理。

## 7. Agent 执行纪律

- 启动时先读本文件，再读 `project-roadmap.md` 的 T09 摘要和引用规范；不依赖聊天
  记忆或未提交草稿。
- 先提交 T09.2，再提交 T09.1，最后提交 T09.3 文档收口；每个提交说明列出文件、
  覆盖范围、命令、网络情况和风险。
- 不修改工作树中已有的 `docs/design/method-verification-pipeline.md` 修改或
  `.gitignore`，除非另有明确任务授权。
- 不等待中途审计或确认。只有发现会迫使修改冻结边界的硬阻塞时才停止，并在本文件
  进度表记录阻塞原因；普通测试失败必须继续修复并重跑。
- 不用放宽断言、降低门槛、缩小分母、删除失败场景或更新 golden 的方式制造通过。

## 8. 最终审计清单

维护者在全部实现提交后一次性执行：

- [ ] T09.2、T09.1 各自有独立提交，提交范围没有混入 T10、ethclient、WSS、
  reference policy、capture 或 golden。
- [ ] 23 个注册方法全部覆盖，注册表和测试互锁双向通过。
- [ ] 四类字段生命周期不变量全部有失败断言，且能定位方法/字段。
- [ ] 八类决策场景全部覆盖，状态、分母、门槛、代表源、Faults 和 typed 结果均
  有断言。
- [ ] 不存在跨来源拼接、静默删字段、故障分母缩小、动态门槛下降或错误升格成功。
- [ ] `go test ./... -count=1`、`go vet ./...`、必要的 `go test -race` 通过。
- [ ] 离线测试无网络 I/O，工作树没有未登记样本或人工生成证据。
- [ ] 文档没有把 T09 合成 regression 写成 provider 兼容性、共识、canonicality
  或 finality 证明。
- [ ] 进度表、路线图和提交说明一致，未解决问题已转为后续任务。

## 9. 进度表（唯一状态记录）

| 阶段 | 状态 | 提交 | 审计信息 |
| --- | --- | --- | --- |
| T09.2 字段生命周期不变量 | 未开始 | — | — |
| T09.1 决策场景矩阵 | 未开始 | — | — |
| T09.3 最终收口 | 未开始 | — | — |
| T09 整体审计 | 未开始 | — | 维护者在全部实现提交后执行 |

状态更新规则：agent 完成一个阶段后只更新对应行和路线图摘要，不改历史结果；维护者
整体审计通过后将本表和路线图一并标记为完成。
