# 方法落点验证流水线与审计留痕规范

状态：方法验证标准（2026-09-24；部分实现，按 T06 收敛）。T06.0 不变量已于 2026-09-25 冻结，见第 1.1 节。
适用范围：`rpc/methods` 下所有已注册方法（RegisteredMethod）及其后续增改  
关联基准：`docs/design/project-roadmap.md`、`docs/design/moving-tag-matrix.md`、`docs/design/method-contracts.md`、`docs/design/architecture-record.md`、`docs/decisions.md`（0020、0023、0024、0028、0038、0039、0040）

---

## 1. 目标与定位

EthGuard 是多源 Ethereum Execution RPC 客户端，核心价值是在异构、有偏、随时间漂移的外部提供商之间，建立可审计、可重放的交叉证据（provider-backed cross-source evidence）。

本规范规定“必须满足什么”；执行顺序、任务拆分和完成状态记录在
`project-roadmap.md`。方法验证与审计收敛为一套低样板、可追溯的流水线：
1. **统一事实来源**：Policy decorator 组合与实际测试是行为事实；`moving-tag-matrix.md` 是人工对照契约，由测试互锁。不得引入 `MethodContract` 声明式结构、代码生成或通用 `suite.RunAll`（决策 0040）。
2. **闭环审计留痕**：通过离线多源样本回放与 golden 快照，让归一化和仲裁行为的变化在代码审查中以 diff 呈现。
3. **标准与规划分离**：本文件保持稳定的验收规则；roadmap 只引用本文件，不复制规则正文。

### 1.1 已冻结的 T06 不变量

2026-09-25 冻结。后续任务不得重新讨论这些边界。本节区分目标语义和当前实现事实；两者冲突时，以本节加上决策 0039、0040 为准，并修正其他文档。

目标语义：

1. 决策 0039 规则 4e 采用方案 (i)。`eth_call` 的 revert 行为达到门槛即可保持 `Corroborated`；具体 `data` 达不到门槛时扣下该字段。原始字节只经 Uncertain 或审计证据可见。generic Strict 保持单 key 语义。
2. 删减式投影只能删除未佐证字段，不能新增、替换或跨来源拼装。`Decision.Value` 允许这种方法契约声明的投影。迁移期间方法规则索引的 result 列是 `raw` 或 `canonical`：`raw` 仍返回捕获顺序第一人的原文，`canonical` 的数量和成功 DATA 写规范编码，`eth_feeHistory` 写受保护字段的规范对象，`eth_getLogs` 写掩码留下的日志字段的规范数组，`eth_createAccessList` 写掩码留下的 access list 字段的规范对象。迁移完成后才不允许 `raw`，这一步还没有做。见 `architecture-record.md` 与 `rpc.Decision`。
3. dimension evidence 至少区分每个维度的 `Support`、`Reject`、`Abstain`。弃权源留在投票分母，不进入该维度的 Reject，也不用其他来源的值补全。
4. golden 记录删减字段。样本回放保持离线；`expect.json` 是数据，不是测试语言（决策 0040）。
5. 不引入 `MethodContract` DSL、代码生成或通用 `suite.RunAll`。
6. 原始 JSON-RPC error 回放必须区分 data 缺失、JSON `null`、`0x` 和具体 data。`rpc.RPCError` 已有 `Data`；缺口不在该类型。

当前实现事实（与目标语义分开记录）：

1. 未迁移的 generic Strict 仍把获胜簇捕获顺序第一人的来源原值写入 `Decision.Value`，保持单 key，没有删减式投影。`adjudicate` 本身不编码。已迁移的数量和成功 DATA 在唯一一次 Resolve 之后写入规范编码，比较键就是这份规范 JSON。`eth_feeHistory` 也在这一次之后写入规范对象，但比较键仍是 feeHistory 键，不是这份规范 JSON。`eth_getLogs` 也在这一次之后写入规范数组，但比较键仍是现有日志键，不是这份规范 JSON。`eth_createAccessList` 也在这一次之后写入规范对象，但比较键仍是现有 access-list 键，不是这份规范 JSON。`eth_call` 不走 generic Strict：成功结果是规范 DATA，revert 仍是代表源原文（4e 删减除外）。
2. `eth_call` 的公开比较键是 `execution:ok|...`、`execution:reverted|data:abstain:missing`、`execution:reverted|data:abstain:null`、`execution:reverted|data:empty`、`execution:reverted|data:hex:`、`execution:reverted|data:raw:`。缺失与 `0x` 不是同一个键。缺席 data 不制造执行冲突；具体 data 不足门槛时删减该字段并保持已佐证的 revert。
3. `rpc.RPCError.Raw` 在 `Data` 为 nil 时省略字段，在 `Data` 为 `null` 时保留 JSON null。直接构造可以区分缺失与 null（`TestCallRPCErrorRawPreservesDataAbsence`）。
4. `rpctest.FailWithData` 可以原样表达 data 缺失、JSON `null`、`0x` 和具体 data。`present == false` 不写 `data`；`present == true` 原样写入，包括 `null`。旧的 `Fail` / `RPCError` 仍然只有 code 与 message。
5. HTTP 收集在 geth 把缺失和 JSON `null` 都解成 nil 之前保留原始 error 对象。`observationRPCError` 因此把字段缺失记为 `Data == nil`，把 JSON `null` 记为 `Data` 为 `null`。没有这份原始对象时仍走 `ErrorData()`；当前 WSS 帧属于这种情况，不能区分缺失和 `null`。
6. `internal/rpcsample/replay` 按记录文件的原始字节校验 sha256，并还原响应体。三份现有样本已迁入 `<feature>/2026-09-23/`。`revert-data` 有 `expect.json`（schema `ethguard.expect/v1`，`transport: http`）。0039 规则 4e 已写入该 golden：短簇的 Abstain 与 reductive 有值，缺失与 `0x` 仍是不同比较键。`block-receipt-tx` 与 `eip1898-object` 也有 `expect.json`（同一 schema，`transport: http`）。generic Strict 在这两批里记单一 `value` 维度，Abstain 为空，reductive 为 false；只有 `eth_call` 写 execution / data 维度。`safe` / `finalized` 按各自 tag 锚定，不降成 `latest`。pending 最高是 Selected。空 body 的 HTTP 429/403 是 transport fault，不进入比较键。没有归档样本的方法是 `eth_getLogs`、`eth_feeHistory`、`eth_chainId`、`net_version`、`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_blockNumber`、`eth_syncing`、`net_peerCount`、`net_listening`；本轮不为它们合成 provider capture。WSS 上缺失与 `null` 的坍缩仍在。`moving-tag-matrix.md` 的 `Tests` 列已在 T06.1 清理为完整测试函数名，并由 `go/parser` 检查这些函数真实存在。`method-contracts.md` 与 roadmap 中的测试名仍只是说明性引用。

---

## 2. 报文差异的五类分类模型（判定与仲裁基准）

归一化与仲裁不得采用“剪枝或冲突”的两分法，必须遵循决策 0039 的五类模型。第 4 类和规则 4e 已在 `eth_call` 落地；generic Strict 仍是单键：

| 类别 | 判据特征 | 处理策略 | 目标效果 |
| :--- | :--- | :--- | :--- |
| **1. 表示差异** | hex 大小写、JSON 键顺序、空白、QUANTITY 的前导零 | 静默归一（Normalize） | 抹平无害噪声。DATA 的前导零是内容（`0x00` 不是 `0x`），不得归一 |
| **2. 范围外字段** | 厂商扩展字段、比较键之外的信封部分（如 revert 的 code/message） | 不进入比较键（Prune） | 保留在获胜载荷上，作为未佐证上下文，可经 Uncertain 获取，不制造冲突 |
| **3. 不可准入** | 无规范形式、缺必填字段、请求或内容绑定失败 | 记为 Fault | 不提供支持；来源**仍留在投票分母中**（决策 0020、0025） |
| **4. 缺席 / 弃权** | 方法契约**显式声明可弃权**的子维度缺失或为 null（首例：revert 的 `data`） | 该子维度弃权 | 计入其断言的维度；在该子维度上不支持、不反对，也不以他源值补全。具体值不足门槛时按 0039 规则 4e 保留已佐证行为并扣下该字段 |
| **5. 断言冲突** | 准入回答在必需比较维度内给出不同的规范值 | 按现有裁决：门槛不足或一致性模式下分裂即 `ErrConflict` | 保留 Support/Reject 证据；只说明来源之间存在分歧，不断言哪一方正确。可弃权子维度的短簇按规则 4e 处理 |

未声明可弃权的缺失字段：必填的归入第 3 类，比较键之外的归入第 2 类。“协议可选”本身不构成可弃权（决策 0024）。

---

## 3. 方法落地验证流水线（5 阶段机读契约）

任何方法进入或保留在 `RegisteredMethod` 目录，必须依次通过以下 5 个阶段：

```
[阶段 1: 官方规范掩码 (Spec Mask Ratchet)]
                 │
                 ▼
[阶段 2: 动态标签与改写契约 (Moving Tag Matrix & Tests Interlock)]
                 │
                 ▼
[阶段 3: 本地准入与拒绝屏障 (Local Admission & Rejection)]
                 │
                 ▼
[阶段 4: 异构报文归一与内容解耦 (Wire Normalization & Pruning)]
                 │
                 ▼
[阶段 5: 真实样本离线回放与仲裁 (Offline Sample Replay & Adjudication)]
```

### 阶段 1：官方规范掩码与单向阀（Spec Mask Ratchet）
- **依据**：`internal/spec` 固化的官方 `execution-apis`。
- **要求**：
  1. 方法必须在规范掩码覆盖范围内；
  2. 规范未收录的方法（如 `net_listening`、`net_peerCount` 等节点本地方法）必须在 `specAbsentAllow` 中显式登记并写明原因；
  3. 上游规范一旦收录了此前豁免的方法，单向阀测试必须失败，强制移除豁免。
- **代码实现**：`rpc/methods/spec_tables_test.go`。

### 阶段 2：动态标签与契约互锁（Moving Tag Matrix & Static Interlock）
- **依据**：`docs/design/moving-tag-matrix.md`。
- **要求**：
  1. 16 列全部显式声明，不得留空（不适用的写 `n/a`）；
  2. 依赖 `latest` / `safe` / `finalized` 的方法，必须按 matrix 行声明的方式锚定到本次调用建立的 reference（决策 0026）。可以是 hash pin（如 state read 经 `rpc.Anchored`，`eth_getBlockByNumber` 经 `AnchorBlockHashForTag`），也可以是 height pin（如 `eth_getLogs`、`eth_feeHistory`）。不得把移动标签原样发往上游；
  3. 禁止出现 `finality proof`、`canonical proof`、`共识证明` 等越权表述；
  4. `Tests` 列中的测试名必须经 `go/parser` 静态扫描确认对应的 `func Test*` 真实存在。只接受完整函数名；不解析 `Prefix*` 通配、`as above` 或自由文本。`method-contracts.md` 与 `project-roadmap.md` 中的测试名是说明性引用，不构成第二份测试索引。T06.1 已落地该检查，不再只要求单元格非空。
- **代码实现**：`rpc/methods/moving_tag_matrix_test.go`。

### 阶段 3：本地准入与拒绝屏障（Local Admission & Rejection）
- **依据**：畸变参数、非法区块标签、不支持的 pending 形态。
- **要求**：
  1. 不支持的 pending 请求（如 `BlockByNumber(-1)`、`eth_getLogs` 的 pending 范围、`eth_getBlockReceipts` 的各种 pending 形态）必须在本地立即返回 `ErrInvalidRequest`；
  2. 本地拒绝必须零开销：不发起上游请求，不触发 anchor；
  3. 本地拒绝记为 `failed` 事件，`Decision.Status` 为 `Unknown`，不得带成功置信状态。
- **代码实现**：`rpc/methods/*_test.go`、`ethadapter/*_test.go`。

### 阶段 4：异构报文归一与内容解耦（Wire Normalization & Pruning）
- **依据**：各节点返回的原始 JSON。
- **要求**：
  1. Normalize 只生成规范比较形式：白名单外字段从比较键中剪除（获胜载荷保留，供 Uncertain），只有无规范形式时才报错；缺必填字段由 Validate/Admit 拒绝并记入 `Faults`。
  2. 按第 2 节五类模型处理差异。空 revert 的缺失 data 与 `0x` 不预先等同（决策 0023）；`eth_call` 的短 data 簇按 0039 规则 4e 通过方法专属 resolver 处理。
  3. 允许方法契约声明的**删减式投影**：可以从已准入结果中扣除未佐证字段，但不得新增、替换或跨来源拼装字段。投影来源和被扣除维度必须进入 dimension evidence；普通方法仍返回来源原值。
  4. Block、Receipt、Transaction 的内容必须与头部承诺或请求绑定校验，禁止跨块内容混入；空 uncle 列表、空 receipt 数组保持合法。
- **代码实现**：`rpc/methods/content_compat_test.go`、`rpc/methods/eth_call_revert_test.go`、`content_boundary_test.go`。

### 阶段 5：真实样本离线回放与仲裁（Offline Sample Replay & Adjudication）
- **依据**：脱敏的多源原始响应样本。
- **要求**：
  1. 统一由 `internal/rpcsample/replay` 加载，并校验样本 sha256；
  2. **Policy 层**：样本按生产路径构造 `rpc.Observation`（错误响应带 `RPCError`），送入对应方法的真实 Policy，断言 `Decision.Status` 或 `ErrConflict`；
  3. **端到端层**：经 `rpctest` 原样返回记录的 JSON-RPC 响应体，走 `Aggregator.Aggregate`，覆盖 Admit 与 typed-read 门禁（决策 0038）。只测 Policy 层会漏掉这两步。`rpctest.FailWithData` 原样表达 error data；HTTP 转换路径保留缺失和 `null`。WSS 仍没有原始帧，不能作这四态的回放传输；
  4. 禁止外部网络 I/O；进程内 `httptest`（`rpctest`）允许。
- **代码实现**：`internal/rpcsample/` 及 `rpc/methods/*_test.go`。

---

## 4. 真实样本归档与审计快照（Audit Artifacts）

为支持低摩擦的社区贡献并提供审计依据，样本按以下规范归档。

### 4.1 目录组织
```
internal/rpcsample/testdata/<feature-or-method>/<batch-id>/
├── index.json             # cmd/rpcsample 生成：每条记录的 request/source/时间/sha256（防篡改凭据）
├── batch.json             # 批次元数据：采集说明、fork、可得时的区块高度、web3_clientVersion
├── expect.json            # golden 快照：每源比较键、维度证据、删减字段、Faults、Decision 及所用裁决配置
└── records/
    ├── <case-id>--s1.json # 提供商 1 原始响应（脱敏，含原始请求）
    ├── <case-id>--s2.json # 提供商 2 原始响应（脱敏，含原始请求）
    └── ...
```

请求参数已保存在每条 record 的 `request` 字段中，`batch.json` 不再重复。现有 `revert-data`、`block-receipt-tx`、`eip1898-object` 原样迁入各自批次，文件内容与哈希不变。

### 4.2 防篡改与变更留痕
1. **脱敏**：清除 URL、API Key、私网 IP、userinfo 与 query 值，只保留来源代号（`cmd/rpcsample` 的 redaction 规则）。
2. **不可篡改**：已入库的 record 与 `index.json` 除脱敏修正外不得修改。协议硬分叉带来新格式时，新增批次，保留旧批次以验证向后兼容。
3. **客户端身份是上下文，不是归因**：尽力记录 `web3_clientVersion`。商业网关背后可能是异构后端池（revert-data 样本中同一端点对相同请求给出了不同形态），不得仅凭网关样本把某种方言归到某个客户端。
4. **Golden 快照**：CI 重新回放，断言结果与 `expect.json` 一致。`expect.json` 必须钉住 corroboration mode、floor 与成员数 N，因为同一组样本在不同配置下结论不同。改变分类的代码变更必须附带 `expect.json` 的 diff，由 reviewer 审阅，不得盲目 `-update`。
5. **社区样本视为不可信输入**：新增或放宽任何归一、剪枝、弃权规则，所依据的样本应当可复现（优先使用 state override 或历史区块哈希等确定性请求），或者来自至少两个独立贡献者，或者维护者能在线重新采到。

### 4.3 Golden 的最小内容与边界

`expect.json` 是数据格式，不是测试语言。每个批次至少记录：

1. `schema_version`、请求标识、成员数 `N`、corroboration mode 和 floor；
2. 每个来源的规范比较键、准入结果和 Faults；
3. 每个受保护维度的 `Support`、`Reject`、`Abstain`，以及最终是否返回该维度；
4. 是否发生删减式投影、被扣除的字段和最终 `Decision.Status` / error。

文件不得包含条件表达式、继承、`Prefix*` 通配或自动生成测试逻辑。改变裁决语义时更新 golden；改变测试函数名时更新 matrix 的完整函数名。

---

## 5. 合规核验清单（PR / Audit Checklist）

新增或修改方法时逐项核验（对应 `project-roadmap.md` 的“每个新方法的进入条件”）：

- [ ] **1. 契约登记与静态索引**：`moving-tag-matrix.md` 完整登记；`Tests` 列使用完整函数名并经静态检查确认存在；
- [ ] **2. 规范单向阀**：`spec_tables_test.go` 通过，没有未豁免的规范缺失；
- [ ] **3. 前置拒绝零开销**：不支持的 pending 与畸形入参在本地阻断，没有调用 anchor，没有访问上游；
- [ ] **4. 故障保全与弃权**：准入失败记入 `Decision.Faults`，来源留在分母；弃权只用于契约显式声明的字段，不以他源值补全；短 data 簇按 0039 进行删减式投影；
- [ ] **5. 真实样本回放**：样本经 Policy 层和端到端层回放，`expect.json`（含裁决配置、维度证据和删减记录）已提交并审阅；
- [ ] **6. 词汇合规**：代码与文档中没有“共识证明 / 最终性证明”等越权表述，冲突不写成“链上分歧”。

---

## 6. 参考笔记：可缺席字段模式（未采纳，不构成决策）

记录日期：2026-09-25。本节是设计参考，不是验收规则，也不是决策。它不修改第 1.1 节的冻结不变量，不修改决策 0039、0040 的边界，不授权任何实现。触发条件出现之前不得据此写代码。

### 6.1 单键模型的前提与其边界

通用路径的一致性建立在一个前提上：一份响应可以被压成一个比较键。在这个前提下三者互不干扰——比较键只用于比较，原始载荷不被改写并成为返回值，投票分母在 Prepare 阶段固定且不被失败缩小。一个来源要么给出一个完整断言，要么整个成为 Fault。

这个前提在"部分断言"处失效：一份响应里某些维度所有来源都断言了，另一些维度只有部分来源给出值，而且没给出值并不表示反对。把这样的响应压成单个键，会把"未表态"和"表态为不同值"混成同一件事，分母模型随之失效。

### 6.2 判据：缺席能否被响应之外的规则解释

同一个"字段有时在、有时不在"的现象有两种处理，判据是该字段的缺席能否被一个独立于响应本身的规则所解释。

- 能解释的，归准入。receipt 与 block 中随分叉出现的字段（如 `withdrawalsRoot`）属于这类：区块号与 fork 调度表可以从外部判定该字段应不应该出现。应出现而缺失是 Fault（第 3 类），不应出现而缺失是正常。决策 0024"协议可选本身不构成可弃权"挡的就是这类情况。
- 不能解释的，才需要维度。`eth_call` revert 的 `data` 属于这类：缺失与给出空字节都是现实中的合法响应，取决于客户端与网关方言，协议无法判定哪一个"应该"发生。没有外部参照，就不能放进准入，只能放进可弃权维度（0039 第 4 类）。

因此 `eth_call` 是特例，不是因为 revert 特殊，而是因为它的 data 缺席无法被外部规则解释。

### 6.3 候选模式

若第二个方法跨过 0024 的门槛，与其把多维裁决提升为框架（那会成为 0040 拒绝的声明式方法契约），不如把"可缺席字段"收成一条窄规则，通用单键路径保持不变：

1. 一个方法可以声明其某些字段可缺席。归一化时这些字段不进入主键。
2. 投票先按主键进行，与现行 generic Strict 完全相同。
3. 主键通过后，对每个可缺席字段单独统计：断言同一值的来源是否达到本次调用的门槛。达到则保留；不足则从返回载荷上删除该字段，并在维度证据中记录谁给了什么值、谁没有给。
4. 删除仍是删减式投影：只能移除未佐证字段，不得新增、替换或跨来源拼装。

这与 `projectRevertData` 现做的事情相同，差别只在它不再是 `eth_call` 的专属代码。receipt 的 `l1Fee`、log 的厂商字段、feeHistory 的 reward 百分位若将来确实需要，都落在这个模式里。

该模式覆盖不了 `eth_call` 的全部。`eth_call` 的主键本身有两种合法形态（成功返回值与 revert 错误体），且 revert 需要从 code 与 message 判断"这是不是一次执行观察"。可缺席字段只消掉 data 那一半，消不掉 `normalizeCallOutcome` 里的分类逻辑。

### 6.4 为什么现在不实现

这个模式当前没有第二个使用者。receipt 的可选字段已被 fork 规则解释，feeHistory 与 logs 用单键加准入即够。为一个使用者提取模式，接口形状是猜的；等第二个具体案例出现再定接口，比凭 `eth_call` 一个案例推测更可靠。在那之前，特例是更诚实的做法：复杂度留在唯一拥有它的地方。

触发条件：出现第二个其字段缺席无法被响应之外的规则解释的方法。届时用两个具体案例决定接口，并另立决策；本节不替代该决策。
