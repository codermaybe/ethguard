# 方法验证流水线与测试所有权

本文件拥有已注册读方法的验证阶段、字段生命周期、决策场景与测试归属。
运行时职责见 [codebase-topology.md](codebase-topology.md)，resolver/reference
边界见 [resolver-seam-contract.md](resolver-seam-contract.md)，捕获文件与 golden
机制见 [capture-golden-contract.md](capture-golden-contract.md)。当前覆盖和缺口只在
[method-transport-coverage.md](method-transport-coverage.md) 与
[project-roadmap.md](project-roadmap.md) 维护，不在此保留阶段完成记录。

## 1. 事实来源与安全边界

- 运行时的读方法声明与 resolver 绑定由 `MethodSpec`、`CompilePlan`、`ReadPlan`
  和真实测试互锁；旧 decorator 链不是第二份现行执行规范。
- `moving-tag-matrix.md` 是人工对照契约；`method-rules.md` 是代码的只读生成投影。
  二者不是第二套运行时方法模型。不引入决策 0040 否决的 `MethodContract` DSL、
  通用 `suite.RunAll` 或第二份运行时字段注册表。
- `Corroborated` 只表示 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 finality proof。冲突只说明来源分歧，不判定谁正确。
- 投票成员在 Aggregate 入口、Prepare 与准入之前冻结。故障、跳过、弃权和
  EncodeCheck 失败不得缩小分母、降低 floor 或用另一来源补齐字段。
- `Decision.Value` 按方法契约规范编码。删减式投影只能删除未佐证字段，不能新增、
  替换或跨来源拼装；来源、被扣除字段和维度证据必须可追踪。
- `eth_call` 的 execution 已达门槛而具体 revert data 未达门槛时，按决策 0039
  规则 4e 保留已佐证的 revert，扣下 data；原始字节只经 Uncertain/审计材料可见。
  generic Strict 仍是单键，不因此变成通用多维框架。

## 2. 五类差异

归一化与仲裁不能只分成“剪枝或冲突”（决策 0039）。

| 类别 | 判据 | 处理 |
| --- | --- | --- |
| 表示差异 | hex 大小写、JSON 键顺序/空白、QUANTITY 前导零 | 规范化；DATA 前导零是内容，`0x00` 不等于 `0x` |
| 范围外字段 | 厂商扩展、比较键外的 envelope（如 revert code/message） | 不制造比较冲突；按 return-value 契约进入 Extensions 或 Diagnostics，不自动成为受保护 Value |
| 不可准入 | 无规范形式、缺必填字段、请求/内容绑定失败 | Fault；来源仍在分母 |
| 缺席/弃权 | 方法显式允许弃权的维度缺失或 null | 只对该维度 Abstain，不是 Reject，不用他源补全；短簇按 4e 删减 |
| 断言冲突 | 已准入响应在比较维度给出不同规范值 | 按现有 mode/门槛裁决，保留 Support/Reject；不足时冲突，不降级求成功 |

“协议可选”不自动等于“可弃权”（决策 0024）。未声明可弃权的缺失字段，
必填的归不可准入，比较键外的归范围外。分叉 requiredness 由对象规则决定。

## 3. 五个方法验证阶段

任何方法进入或保留在 `RegisteredMethod` 目录，都需要以下验证。真实 provider
样本是否已有、哪些 case 未观察到，必须如实登记，不能用合成 JSON 关闭捕获缺口。

### 3.1 官方规范掩码与单向阀

- `internal/spec` 固化的 `execution-apis` 是规范掩码来源。
- 未被上游规范收录的方法（如部分 `net_*` 方法）必须在 `specAbsentAllow`
  显式登记理由；上游收录后，单向阀必须失败并要求移除豁免。
- 实现入口：`rpc/methods/spec_tables_test.go`。

### 3.2 Moving-tag 契约与静态互锁

- `moving-tag-matrix.md` 的 16 列全部显式填写，不适用写 `n/a`。
- 移动标签按该方法声明的 hash pin 或 height pin 改写到本次 invocation 建立的
  reference，不能直接把移动标签发往业务上游；`safe`/`finalized` 不降为 `latest`。
- `Tests` 列只接受完整 `func Test*` 名称，由 `go/parser` 确认存在；不接受
  `Prefix*`、`as above` 或自由文本。其他文档的测试名只是导航，不是第二份索引。
- 实现入口：`rpc/methods/moving_tag_matrix_test.go`。

### 3.3 本地准入与拒绝

- 按方法契约拒绝不支持的 pending 和畸形请求；例如 logs 的 pending 范围与
  block receipts 的 pending 形态不能触发 reference 或业务请求。
- 本地拒绝必须零上游、零 anchor，返回 `ErrInvalidRequest`，记为 `failed` 事件，
  `Decision.Status` 为 `Unknown`，不附成功置信状态。
- 方法级校验位于 `rpc/methods/*_test.go`；公开 typed/facade 结果在
  `internal/verification` 验证。允许的 pending-local 与拒绝是不同语义。

### 3.4 Wire normalization 与内容绑定

- Normalize 产生规范比较形式；必填字段与请求/内容绑定由 Admit/EncodeCheck
  保证，失败记入 Faults，不能因省略字段而制造簇内相等。
- Block、Receipt、Transaction 的内容必须与请求或已验证承诺绑定；不得跨块拼装。
  空 uncle 列表和空 receipt 数组保持合法。
- `eth_call` 的 data 缺失、JSON null、`0x` 和具体值必须分开；缺失与空字节不
  预先等同。删减与 execution/data 证据遵循 §1 和 resolver seam。
- 重点入口：`rpc/methods/content_compat_test.go`、`eth_call_revert_test.go`、
  `internal/verification/integration/content_boundary_test.go`。

### 3.5 真实样本的双层离线回放

- `internal/rpcsample/replay` 从原始记录加载并校验 SHA-256。
- Policy 层沿真实方法路径构造 `rpc.Observation`；错误响应带 `RPCError`，检查
  status、维度和冲突，而非只检查最终值。
- 端到端层经 `rpctest` 原样返回已记录 JSON-RPC body，进入真实 Aggregator、
  Admit 与 typed-read 门禁。只测 resolver 会漏掉后两者（决策 0038）。
- `rpctest.FailWithData` 可表达 data 缺失、null、`0x` 与具体 data；传输是否保留
  presence 要单独验证。普通 golden 只接受 HTTP，不能据此断言所有 WSS adapter
  都已保真或 provider 四态证据已闭合。
- 禁止外部网络 I/O；允许进程内 `httptest`。文件结构、配置快照、脱敏与覆盖等级
  统一见 capture/golden 契约，不在此维护副本。

## 4. 字段生命周期与决策场景

这些是持续验收规则，不是已完成阶段的施工顺序。

### 4.1 字段生命周期

```text
raw response → Admit → Normalize / comparison key → EncodeCommit
             → Decision.Value / typed projection
```

每个受保护字段必须可归入 `required`、`optional`、`derived`、`Extensions`、
`Diagnostics`、删减式投影或明确的 admission/encode fault；不允许未记录的字段消失。
字段进入 Value 的依据只有比较键、请求绑定、已验证承诺、公开派生规则四类，
详见 [return-value-design.md](return-value-design.md)。编码器输出不是第二份字段表。

每个已注册方法至少覆盖：

1. **表示变形**：键顺序、空白、允许的 hex 大小写/`0X`、JSON 字符串转义和扩展
   字段重排不改变比较键与规范 Value；同一变体轮换 pool 位置时 Value 逐字节一致，
   代表源仍按捕获顺序选择。区块/交易 typed wire decoder 不接受的转义 hex 属于
   admission fault，不要求将非法输入视为等价变形。
2. **受保护字段变异**：删除、篡改、错误绑定、非法类型与超范围值在 Admit 或
   EncodeCheck 失败；不从另一来源补回，不通过省略来制造相等。决策 0061 的
   本地整答案方法只做顶层 wire 准入，syncing 对象坏嵌套类型由 typed projection
   拒绝，不要求 Admit 拒绝；不能借此暗中扩大为逐字段 syncing 契约。
3. **往返**：对每份通过检查的来源，`typed(raw) == typed(Encode(raw))`；
   派生字段从受保护内容按公开规则重算。
4. **守恒**：逐 observation、逐数组元素递归追踪到 Value、Extensions、Diagnostics、
   删减式投影或可分类 fault。域外/omitted 字段不得进入 Value；例如 block 的 size
   与内嵌交易元数据按已有 encoder/schemaOmitted 边界处理，不从另一来源补回。

优先复用现有 encoder、Policy、Aggregator 和 typed adapter，不建立第二套 decoder。

无独立 typed adapter 的方法使用现有生产 projection/Value 路径并明确边界，不新造
adapter，也不以“暂不适用”掩盖无法审阅的结果。往返断言经真实 Client/adapter，
不使用测试侧摘要 decoder。`eth_blockNumber` 的注册 resolver Value 与公开
`Client.BlockNumber` 从 reference 派生的结果是不同验证面。

Registered inventory 与生命周期、场景声明必须双向一致；缺行、重复行、失效测试名
均失败。官方 schema 精确路径集（含嵌套字段）与生命周期/omitted 声明互锁，
新增字段缺断言必须失败；无 pinned result schema 的 peerCount/listening 显式标记
noSchema，不建立第二份运行时字段表。失败报告定位到方法和字段，而非只报总数。

### 4.2 决策场景矩阵

`RegisteredNames()` 每个方法至少覆盖 `all-agree`、`minority-dissent`、`outlier`、
`single-fault`、`all-fault`、`conflict`、`notfound`、`pending/local` 八类，并以
`quorum-boundary` 独立检查门槛。现有测试把 pending/local 表达为 `pending` 行，
共九个显式行；每个方法逐行调用断言 helper，AST 互锁要求名称各一次、调用各一次，
不用遍历所有方法/场景的通用 runner。静态互锁不替代运行时语义断言。断言同时覆盖：

- `Unknown`、`Conflict`、`Selected` 及方法允许的 `Corroborated`；
- 固定分母下 Support、Reject、Faults、各维度 Abstain；
- 代表源与 Value 的同源/规范编码关系；
- Policy、真实 Aggregator 和 typed adapter 的公开结果。

场景矩阵是进程内合成证据，不进入 provider capture 目录、不产生 provider
兼容性结论。错误、admission failure 与 reference 结果必须沿生产边界构造，
不能用 mock 绕过实际门禁。EncodeCheck、Admit、typed decode 失败不得改走
单源值；`Selected`/`Forwarded` 只能由已有方法契约产生，不得为测试方便升格。

合法异值是 dissent，不是 fault；合法 null/空结果/notfound 不等于 malformed。
不适用的场景也保留显式行并说明契约结果，不能为所有方法强套 Corroborated。
本地 syncing 矩阵只用合法 false，不合成 progress object；没有第二合法形态的
minority/outlier/conflict 行说明原因，不伪造 provider 拒绝或关闭真实对象捕获缺口。

固定 N、floor 与 Q 要独立核验门槛两侧，例如 N=5、floor=2 时 2 个支持者不足、
3 个支持者达到门槛；不只比错误文字。Unknown 行只有 conflict 且显式声明 ValueFrom
时可保留簇 Value；all-fault、notfound、本地拒绝和 ValueFrom<0 的行断言空 Value。
按实际方法契约区分 all-agree、合法少数异见、全故障与 pending-local 的状态，不通过
删除 fault 来源、放宽 mode 或下降动态门槛制造成功。

## 5. 规则索引的只读投影

`rpc/methods/method_rules_test.go` 的 `TestMethodRulesMatchIndex` 渲染
`method-rules.md` 并逐字节锁定；只有显式更新操作可以改生成物，测试默认不写文件。
`TestAdmissionIndexRow` 另行互锁 admission 声明，不生成该文档。

| 列 | 唯一事实来源 |
| --- | --- |
| 方法 | `Catalog()` 的已注册方法 |
| anchor、block-ref、pending | 方法声明与 ReadPlan 投影 |
| resolver、compare kind、key source、result | resolver 的 MethodRule 与计划绑定互锁 |

计划路径不遍历 decorator 链。内省只报告构成，不参与 Prepare、Resolve 或网络调用。
不为任何注册方法开放“允许未声明”的例外。索引是导航，不展开 `eth_call` 的
code/message 分类、revert data 分支或对象字段表。

`pending` 只用三种互不混淆的语义：`local-observation`（本地观察，Selected
封顶）、`local-reject`（零上游拒绝）、`none`。

## 6. 测试归属与共享设施

### 6.1 黑盒与白盒

跨模块 facade 验证归 `internal/verification/facade` 与 `integration`；生产包中
只保留确实需要未导出状态、内部函数/状态机 invariant 或合理强耦合断言的白盒测试。
不为迁移测试而导出生产符号、公开阈值常量或建立测试专用生产接口。

`scripts/whitebox_inventory.sh` 可以辅助归属审查：它对未导出顶层声明做词边界
匹配，注释也可能计入，因此结果会高估依赖；零匹配是候选，不替代人工语义审查。
按依赖性质处理，而非维护会过期的逐文件数量表：

| 依赖 | 归属判断 |
| --- | --- |
| 内部 normalization/admission、invocation 状态机、故障归因、digest/acceptance invariant | 保留拥有该实现的包内白盒断言 |
| 无未导出依赖 | 可以通过公开 seam 黑盒验证 |
| 仅共享未导出 fixture | 整组判断；不机械拆开单个文件，不因此扩大生产 API |
| 已删除执行模型的 oracle/constructor | 保留仍适用的业务断言，移除失效等价模型；不把历史兼容形态变成新规范 |

### 6.2 Module 与 fixture 边界

- integration 的 fleet、chain、field rows、typed probes 和 scenario harness 保持
  局部所有权；不按历史 T09 编号或文件数拆永久 package。
- `rpctest` 拥有共用 HTTP 节点，`internal/testkit/wstest` 拥有 WSS 对端；
  不再创建另一套相同设施。replay 由 `internal/rpcsample/replay` 拥有。
- 新 contracts/scenarios Module 必须有不同的公开验证 seam；共享 fixture 至少有
  两个真实 Module 消费者，并能收窄为隐藏节点、Client 构造与清理的小 Adapter。
  不能让两个 Module 互调未导出 helper，也不能建立第二套 typed/execution 链。
- 生产代码不得导入 verification 或 testkit。外部 RPC／Anvil 的未来分层设计见
  [external-rpc-test-framework.md](external-rpc-test-framework.md)；本地 JSON／故障注入、Anvil 输出与离线回放通过均不能单独充当真实 provider 观察证据。

## 7. 变更审阅清单

- 方法登记、moving-tag 行、完整测试名与 spec 豁免单向阀一致。
- 本地拒绝零上游；reference 失败不发业务请求；失败状态、分母和故障证据完整。
- normalization、字段生命周期、决策矩阵、代表源与 typed 三层断言齐备。
- 真实样本/golden 的变动可解释；未观察到的 provider 形态如实保留缺口。
- 报告实际覆盖的方法、失败路径、执行过的命令、是否改样本/golden 和剩余风险。
  全量、race、vet 与 release 门统一服从 [release-contract.md](release-contract.md)，
  不以历史阶段验收记录冒充本次运行结果。
