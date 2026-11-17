# 项目路线图与新会话入口

本文可直接用于重启对话：说明当前基线、下一步、执行顺序和未完成事项，不依赖旧聊天、`docs/tmp/`或`/tmp`材料。具体业务合同由末尾链接的正式文档维护。

**后续顺序：沿整体数据管线从前向后，先完善公共前置与基础流程，再逐个方法精调行为和性能。** 不按零散发现随意切换模块，不同时启动多个改造方向。

## 1. 当前基线

- 最新实现保存于`8e0e77a`：AccessList复用已解析字段和生成标量构造器，直接使用生成结果的marshaler；共享codec增加有界短ASCII字符串快路及独立测试。本文随后另存文档提交；新会话先用`git log`和`git status`核对实际HEAD与工作树，不覆盖用户已有改动。
- 项目仍固定Go **1.26.10**。Go1.27.3只做过隔离评估，未改`go.mod`、生产JSON import、生成器或生成资产。**Go1.27暂不引入**，后续整体评估，收益不计入现有实现优化。
- 现行设计文档系列、公开`ethapi`类型迁移和首轮逐模块整理已完成。目标仍为固定go-ethereum v1.17.10的47项目标方法名称集合，已有27个注册读RPC以及独立写入、订阅和派生能力；这些数量不代表完整兼容。
- aggregator、resolver、pool、transport、runtime及facade的首轮等价整理和审计修复已保存，但不代表整条管线或全部方法已完成设计优化。
- 两个交易查询与单收据strict canonical路径已接入。receipt复用已校验canonical字节；AccessList仍使用现有方法规则和生成模型，不以新增processor数量衡量完成度。
- FeeHistory已共享解析函数，但各阶段尚未复用同一解析结果。共享Log的单遍尝试已撤回，恢复基线的Validate与EncodeOne两段处理，以保留准入、别名和错误优先级。

### 已知成本与验证边界

固定Go1.26.10，本轮AccessList的64项对象编码约由1.160ms降到0.829ms，分配次数约3638降到2480；真实Aggregate的1/3/5来源大列表耗时约下降9%/12%/13%。这些是本地固定fixture的对照，不是生产延迟或CPU使用率。相对最初手写编码仍有成本差距，尚未接受所有性能回退。

变更相关全仓测试、race、vet、build、tidy、架构／方法互锁、生成新鲜度及字符串／AccessList各10万次定向race fuzz通过。65项差分矩阵的接受、输出字节和本矩阵错误文本与原基线一致；有限矩阵不等于完整输入域证明。

额外JSONsafe race fuzz完成100万次。额外wire normalizer race fuzz在最后报告654606次、运行超过12分钟后人工中止；未观察到反例，**不算通过，仍待独立诊断和完成**，不直接归因于环境或新代码。未进行本轮真实provider采集、Anvil重跑或正式发布。历史证据不代替未来目标提交的验收。

可复现入口在已跟踪源码：

- 对象基准：`rpc/internal/objects/accesslist/bench_test.go`；归因基准：同目录`cost_decomposition_bench_test.go`。
- 真实聚合基准：`aggregator/accesslist_route_bench_test.go`，覆盖空／典型／64项和1/3/5来源；同一fixture保留冻结旧encoder控制组。
- 独立字节对照及fuzz：`internal/ethcodec/helpers_test.go`；条目接受域与顺序反例：`rpc/internal/objects/accesslist/acceptance_domain_test.go`。
- Log真实消费者反例：`internal/typed/log_alias_projection_test.go`、`rpc/internal/objects/receipt/log_alias_test.go`。

临时日志可以缺失；根据以上基准、测试和Git历史重新验证即可，不能因临时文件不存在阻塞接续。

## 2. 阶段一：先完善公共数据管线（下一步）

管线概览：调用输入 → 请求准备／冻结 → 执行计划与reference → 来源调度／采集 → 逐源处理 → 聚类／裁定 → typed结果、证据与最终记账。

这是审查数据依赖的顺序，不是要求机械移动现有检查。先沿真实调用链说明每段输入、输出、谁拥有数据、何时需要复制；复用已完成的工作，保留必要的独立检查与错误阶段。方法专属schema／绑定／算法知识继续由方法和对象模块拥有。

| 顺序 | 工作 | 完成条件 |
| --- | --- | --- |
| P1 | 调用入口、参数检查、所有权与冻结 | 调用方值不被修改；用户marshaler不重复执行；语义请求、wire参数与记录保持一致；失败在正确阶段返回 |
| P2 | 计划、模式、reference和路线冻结 | 声明与实际能力一致；应建立reference时只有一条明确路径；失败不发业务请求；后续消费冻结事实，不反复推断 |
| P3 | 来源选择、调度、采集与原始响应交接 | 区分未尝试、gated、传输失败与返回值；固定成员分母；原文保真，重复信封处理／数据转换有明确理由 |
| P4 | 逐源结果到裁定、交付与最终记账 | 复用已验证表示；梳理canonical构造、getter、分簇、排名、commit间的复制；内部只读消费与对外独立副本分清；fault、健康、证据和清理各有唯一完成责任 |

**新会话从P1开始。** 先读`execution.go`的`operation`、`internal/runtime/{runtime,composition}.go`、`rpc/read_plan.go`及`aggregator/{aggregator,phases,invocation}.go`，按实际数据流进入pool／transport，再进入rpc结果消费。仅阅读当前段涉及的正式合同，不重新执行已完成的全系列文档整理或公开类型迁移。

阶段一不要求新建通用框架、大型处理结果对象或公开API；已有ReadPlan、CanonicalResult等表示优先沿用。发现相似代码先核实是否重复做同一工作，不以helper合并或源码行数减少代替收益。可以选择方法作为管线验证样本，但不提前展开该方法的全面精调。

阶段一完成后，公共流向、数据交接和生命周期应清楚稳定，并有真实调用链下的正确性和成本证据；再进入阶段二。

## 3. 阶段二：逐个方法精调行为和性能

公共管线齐备后制定一份有顺序的方法清单，一次收口一个方法或紧密关联的方法族，再进入下一项。每项先明确现行行为与需要裁决的差异，再精调实现；保留独立正反例及对象／真实聚合两层测量。

| 方法方向 | 尚需处理 |
| --- | --- |
| 标量与本地观测 | 逐入口参数、结果、错误及Selected上限；net_version零值与正值限制、local Provenance／无可尝试来源／identity优先级核实 |
| FeeHistory | 让准入、请求count／reward绑定和编码消费准备结果；停止生成后丢弃比较键；精确Number／ratio gate、supported子区间及optional presence分别裁决 |
| AccessList | 保持已完成去重，继续评估生成模型构造与序列化成本；官方顶层可选与现行必需、null折合是方法差异，不暗改接受域 |
| Log／Receipt | 区分精确键准入和编码别名语义，保持绑定及错误顺序；query、receipt、typed订阅和Pull都要验证；不恢复已证不等价的单遍实现 |
| Transaction／Block／Header | 复核剩余内部视图、对象准备及转换；保留私有geth的签名、hash、bloom、trie／body／uncle承诺检查，不重做已完成的公开类型迁移 |
| CallRevert／Median／Local／SimulateV1 | 各自裁定与错误presence、请求关系及typed投影；不套统一Strict成功公式；Simulate的完整模拟语义及provider观察仍未完成 |
| 写入／订阅 | 冻结字节、来源尝试、未知结果、轮询／同源Pull／恢复与资源清理；保持现有合同，未决政策移至阶段三明确处理 |

保留项也须明确说明理由。生成模型存在不代表可删方法规则；解析函数共用不代表同一响应只解析一次；typed边界的独立检查不自动属于浪费。

## 4. 阶段三：独立运行期专题与工具链评估

| 专题 | 当前事实与未决项 |
| --- | --- |
| F1 freshness | 已有请求scope和tip样本过期机制；默认lag、样本窗口／最低覆盖、豁免、结构化原因及finalized／单源组合未定，不能从零重新实施或自行选默认值 |
| S1订阅 | 已有快照、会话、固定门槛、同源Pull及恢复；Close／终止错误、单源模式、预算／背压、generation、恢复范围、诊断和root事件API仍需统一 |
| Go与JSON | Go1.27暂缓引入。已知保留旧JSON API也能受益于新内核，但B/op可能增加；直接使用json/v2默认语义会改变大小写匹配、重复键、UTF-8、空数组和omitempty，需独立验收 |
| 验证维护 | 手工feature／noCapture登记、cell到真实断言的追溯、canary阈值互锁、时间敏感测试及未完成wire大预算fuzz，分别诊断，不用自述表代替独立断言 |

这些专题保持现行行为，不成为阶段一等价整理的隐含改动；需要改变保证时由主控明确目标、影响和验收，再交执行代理实施。

## 5. 阶段四：外部证据与发布验收

补齐实际所需的交易类型生命周期、真实provider方言／状态／订阅观察、跨传输乱序／背压／重连／取消和材料证据。离线、Anvil与真实provider分别记账，既有捕获与日期化证据不改写成新观察。

对最终目标提交执行发布合同的完整门禁、兼容／API报告、构建与产物检查。内部优化、有限矩阵或一次全仓测试通过，均不能推出完整provider兼容或稳定发布已验收。没有明确授权不推送、发PR或发布。

## 6. 所有阶段共同保持的边界

- 官方字段／标量规则归已审计来源和生成层；方法关系及绑定归方法模块；coordinator不解释Ethereum schema。
- 保持冻结成员、分母、裁定门槛、代表源、错误类别与阶段；正常dissent不成为健康失败。每次真实尝试最终记账一次。
- 原始响应、canonical主值、Provenance、Diagnostics和Extensions职责不同，不能互相补字段或冒充来源材料。
- canonical处理只在现有合同允许的路线使用；pending-local／Forwarded保留其语义。Block、Median、Local、CallRevert、写入和订阅不强行套用同一处理器。
- 不为性能引入跨调用缓存、自动重试／写重放，或借新的统一表示改变接受域。必要算法桥、信任边界检查及对外交付隔离继续保留。
- 在已确定合同内连续完成任务；普通失败自行修复。未决政策默认保留当前行为，记录后继续可独立执行的事项，不设等待用户回复的常规中间审计点。
- 主控负责合同和设计判断；轻量模型执行明确的小范围实现、验证、测量与记录。整段确定性工作结束后统一审计，不把各代理的“完成”直接作为验收结论。
- 允许完整重写有效文档，避免多代说明拼接；阶段材料只存`docs/tmp/`并忽略，不新增`docs/design/tmp`，不force-add临时日志。正式合同、当前路线图、必要测试及基准进入Git。

## 7. 正式合同入口

| 需要了解 | 负责人文档 |
| --- | --- |
| 模块职责、ReadPlan与执行路线 | [代码库拓扑](codebase-topology.md) |
| 方法保证、分母、门槛、请求绑定及待裁决差异 | [方法合同](method-contracts.md) |
| Value、presence、typed、原文与附属材料 | [返回值合同](return-value-design.md) |
| 官方来源、勘误、生成及新鲜度 | [生成合同](wire-codegen.md) |
| tag／pending与传输能力 | [引用矩阵](moving-tag-matrix.md)、[覆盖清单](method-transport-coverage.md)、[兼容政策](go-ethereum-compatibility-policy.md) |
| F1／S1 | [reference合同](reference-freshness-policy.md)、[订阅合同](subscription-contract.md) |
| 行为证据与发布检查 | [验证合同](method-verification-pipeline.md)、[发布合同](release-contract.md) |
| 历史原因 | [决策档案](../decisions.md)，ID和日期保留，旧Status不自动支配当前实现 |

`Corroborated`表示provider-backed cross-source evidence（配置provider之间的佐证），不是Ethereum consensus、canonical chain或cryptographic finality证明。正式行为以各owner合同为准；本文确定接续顺序与工作状态，不成为第二份方法规范。
