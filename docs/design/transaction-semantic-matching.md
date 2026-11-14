# Transaction 方法级 key 兼容修正与官方结果收敛

状态：**首个实施检查点已落地；双 switch 收敛与全方法自治组织均已完成（2026-11-12）**。方法是规则的归属范围，官方直接字段 key 是最小修正单元；外层按 key、内层按该 key 的完整形态／状态集中展示已审核规则，不向 key 内部的局部表示或语义继续细化。主聚合只覆盖官方结果字段；外部／vendor key 沿用已有附属比较与证据交付。已实施范围与验收证据见第 12 节，不表示完整 provider 兼容或全状态覆盖。

现行实现合同：[返回值合同](return-value-design.md)、[方法契约](method-contracts.md)、[字段与行为验证](method-verification-pipeline.md)。本文拥有该切片的规则与证据边界；跨方法知识归属见 [拓扑](codebase-topology.md#方法自治组织)，推进方向见 [路线图](project-roadmap.md)。临时施工计划、评审输入及第三方原文不纳入版本保存。缺字段反例与回归要求在本文保留，未改变的 Core、证据、固定成员与公开 API 合同继续有效。

## 1. 目标与非目标

在现有流程内增加**方法级、官方 key 维度的有限兼容修正**：既不机械地只接受官方规范拼写，也不为了迁就所有 RPC 无限放宽。读得懂且有已审核修正的，转换成符合官方定义的候选值；读不懂或修正后仍不能满足合同的，拒绝该来源回答，不继续向更深层追逐方言。

- 官方 key 命中规则：明确等价条件并归一，进入主聚合比较。
- 官方 key 未命中修正规则：保留该字段在官方定义范围内的完整值，校验通过后继续原有比较；未命中不是忽略字段，也不是绕过校验。
- 非官方 key：复用已有附属字段比较与 uncertain／证据交付，不加入主比较键，不改变官方结果的支持集合或业务结论。
- 未经获准修正且不满足官方或方法合同的输入按准入规则拒绝；修正也不能掩盖错误声明。外部字段的合法差异本身不构成主结果 Fault。
- 主裁决仍针对每份来源回答的官方比较结果整体分簇、计票，不逐字段投票或跨来源拼接 Value。

首个实现切片仅覆盖独立 transaction：`eth_getTransactionByHash` 与 `eth_getTransactionByBlockHashAndIndex`。完整 block 的内嵌 transaction 后续单独接入，不直接复制独立交易的上下文和派生规则。

规则允许直接位于 `rpc/methods` 对应方法文件及其具名内部函数中，继续由现有 `MethodSpec` 装配。可复用既有对象 owner 和通用 codec，确实相同的规则才提取共享函数。不要求新增公开 Go API。

不引入 `MethodPolicy`、`EntityDomain`、统一 `EntityNormalizer`、第二份方法注册表、规则 DSL、插件、动态配置、第二份官方字段表、通用框架或新的结果载体。不建立 key 内子路径、数组元素、来源、交易类型或 mined／pending 组合注册规则，不做 key 内部的局部表示修正。不附带 sender recovery、inclusion proof、freshness、传输状态机或性能优化。

## 2. 实施前基线与修复落点

下表记录本切片实施前的基线，不是更新后的缺陷清单。现有直接比较、方法装配、对象准入和附属证据框架继续复用，不另作历史能力审计；对应实现和验收见第 12 节，不扩大主聚合责任域。

| 入口 | 当前行为 | 与目标的差距 |
| --- | --- | --- |
| `internal/jsonsafe` 与 `aggregator/invocation.go` | 已有 JSON 限制及重复 key 拒绝，方法准入前执行 | 兼容路径继续经过现有安全入口，不重建另一套 parser 框架 |
| `rpc/internal/objects/transaction/transaction.go` 的 `Validate`／`Admit`／`AdmitInBlock` | 用 Geth 解码检查交易载荷、自身 hash、非零签名、from 形状与请求上下文绑定 | 不是完整官方 information object 校验；from 未经 sender recovery；不能据此宣布全部 required 字段已覆盖 |
| 同包 `Normalize` 与 `encode.go` 的 `Encode` | 均依赖 `wire.PruneMethod` | 应在官方字段边界内补形态归一并保证官方内容不漏；将非官方字段排除在主比较外本身不是缺陷，原始证据另走已有通道 |
| `rpc/methods/spec_inventory.go` | 两个独立交易方法使用 normalizer key，encoder 单独产生 Value | 更改等价关系必须同步 key 与 Value，不能只改一个 switch |
| `ethapi/types.gen.go` 的 `TransactionInformation` | 已有固定官方 oneOf 分支：legacy、2930、1559、4844、7702 | 应复用官方类型与约束；不能把新 switch 当成替代 schema 的第二份定义 |
| `rpc/internal/wire/prune.go` 的 `PruneMethod` | 通过 `any` 解码、裁剪、hex 规范化后编码，会丢弃 null 成员 | 不能原样作为保真 fallback；须保留官方 presence、null、数组内容和数字精度，不为保留外部 key 而取消主比较边界 |
| `aggregator/aggregator.go` 的来源 Fault 归因 | 记录不可用回答，不自动修改 pool 健康 | 本切片须验证明确合同违规的健康归因及调度隔离接线，不能把 Fault 已记录冒充隔离已生效 |

实施前已记录缺 `blockTimestamp`、`yParity`、`accessList`、`gasPrice` 仍可能通过的反例。这些是合同缺口，不是“未匹配所以原样比较”可以代替的 required 检查；当前检查点用 `TestTransactionRequiredFieldsRejectBeforeVoting` 回归，不据此制造新 provider 观察。

## 3. 三种出口，而不是一个含糊的 bool

以下出口仅针对**当前方法中已识别为官方字段**的修正规则。非官方 key 走既有附属流程。内部规则至少区分 **候选值、是否修正、错误**，无需据此新增公共接口：

| 结果 | 含义 | 本来源如何参与裁决 |
| --- | --- | --- |
| `matched=true, err=nil` | 官方 key 命中已审核的等价规则，得到明确规范值 | 校验通过后，以规范值参与该来源的官方字段比较 |
| `matched=false, err=nil` | 官方 key 没有获准或适用的修正规则；尚不表示原值合法 | 保留完整官方值，通过后续校验才参与比较，不静默忽略 |
| `err!=nil` | 已知约束失败、输入歧义、绑定失败，或无法形成当前保护合同要求的结果 | Fault、不投票、仍占冻结分母；不尝试宽松 fallback |

“没有特殊归一规则”不等于“没有验证规则”。一个官方字段可以未命中语义归一，但仍必须通过其类型、required、nullable、取值和方法绑定检查。

### key 的含义和直观控制流

这里的 **key 是 transaction 对象中由固定 execution-apis 定义的直接字段名**，如 `type`、`to`、`input`、`accessList`。它不是请求参数序号，也不是聚合器内部表示一份官方结果的 comparison key。官方字段身份由现有 schema／投影确定，不由 switch 是否有分支决定。

以下只表达控制流，不定义新的 Ethereum 等价关系，也不是待编译 API：

```text
保留来源原文供已有 source-evidence 通道使用
经过现有 JSON 安全检查，保真解析，按官方定义区分官方与外部内容

从原始同源字段建立只读上下文，候选对象独立写入
for key in 已有官方字段与本方法明确的缺失派生 key 的去重集合:
    当前方法所属的 switch key:
        case 有已审核修正规则的官方字段:
            该 key 内的 switch 完整输入形态／状态:
                case 获准转换的状态:
                    candidate, matched, err = 完整值规则(原值, 原始同源只读上下文)
                default:
                    candidate, matched, err = 原值, false, nil
        default:
            candidate, matched, err = 原值, false, nil

    err 非空：该来源 Fault，不计票，分母不变
    matched 为 true：完整候选值进入候选官方对象
    matched 为 false：保留该官方字段的完整原值；原本缺失仍保持缺失

候选官方对象 → 官方 required/nullable/分支/值约束与方法准入
    同时核对原始声明，不得覆写错误 hash 或冲突声明后计票
    失败：来源 Fault，不计票；按明确故障类别交健康调度处理
通过 → 同一候选对象用于比较键与逐源 EncodeCheck
    编码失败：来源 Fault，不计票，分母不变
既有完整对象分簇与投票 → 检查簇内规范字节一致 → Decision.Value → typed
外部内容 → 既有附属比较／证据流程 → 随 CallResult 交付
```

完整官方约束在兼容之后执行，不能在兼容前机械拒绝已经获准读入的表示；JSON 安全与必须保留的原始声明检查不因此后移或失效。具体接入继续用既有逐源准入、Normalize 和 Encode 入口，不增加新的全局阶段框架。

字段缺失也须保真处理，不因只遍历已有 key 而漏检。缺 required 默认由准入拒绝；只有针对该方法该 key 明确获准、具有同源可验证依据的完整值派生才可例外，不能补 Go 零值或猜测 null。可选字段缺失保持缺失；缺失与 null 的折合须有该 key 的明确依据。

每个 key 只处理一次，不能先补值再把补出的 key 重走修正。上下文不能读取候选对象中的已改值，以免遍历顺序影响输出。双 switch 表达互斥的真实场景，不要求公共状态枚举，也不将简单条件机械拆成状态机。例如 `blockTimestamp` 先进入 key 分支，再区分 missing／null／present；missing 仅在原始位置字段明确为 pending 时派生完整 null，其余上下文不修正、交回官方必填校验与 typed 分类顺序。实际派生条件见第 12 节，收敛已实施并附幂等回归。

## 4. 方法内按 key 集中，不向 key 内部细化

每个方法在自己的实现中以清楚的外层 `switch key` 和必要的内层完整值状态 switch 展示少量兼容规则，可用静态具名私有函数保持可读性。方法是归属范围，不是新注册框架的维度；不能把 transaction owner 内的规则误解为所有 RPC 共用一个全局 key switch。通用 codec 和对象类型继续复用，重复规则确实相同时才抽取。没有额外规则的方法明确保留原始接受域，无需为了外观对称增加空 switch。

交易 type 0／1／2／3／4 的官方分支、required／nullable、签名载荷 hash、请求 block/index 绑定等仍由类型与准入逻辑负责。规则必要时读取同源只读上下文，但不据此生成“key × type × path × context”注册表，也不按 provider 或到达顺序选择规则。

| key 的值 | 允许的处理 | 不允许的细化 |
| --- | --- | --- |
| 官方标量字段，例如 `type`、`to`、`gasPrice` | 对该 key 完整值采用已审核修正；未命中则完整保留并校验 | 按来源或上下文组合建立新注册体系 |
| 官方对象／数组字段，例如 `accessList`、`authorizationList` | 整个 key 为修正单元；复用官方结构解析、校验和保真编码 | 在 key 内针对子路径、局部表示、数组元素或子字段另作兼容修正，或分别裁决后拼接 |
| 非官方／vendor key | 单独比较，进入既有附属证据通道 | 加入主比较键，改变官方结果分簇、支持数、成功与否或 Value |

解析嵌套 JSON、区分官方／外部成员和无损编码属于结构工作，不授权新增内部局部修正规则。官方对象内的非官方成员也走附属流程。某个完整 key 没有安全修正方案，就保留其官方内容并校验；合法则比较，仍读不懂或非法则拒绝，到此停止，不递归扩展兼容层。

缺 `type` 的 legacy、pending context、v／yParity 等问题分别核对官方合同、具体 key 规则与准入义务，不批准一揽子补全。官方语义已明确但生成物表达错误时，修正规范输入／构建／typegen 链路，不让运行时伪造字段迎合错误规则；见 [生成边界](wire-codegen.md)。

### 不能合并的“相同含义”

`0x0000` 与 `0xdeadbeef` 可以在某个**已定义的业务分类**中同属一个结论，但不能据此认定它们是相同的 transaction DATA、QUANTITY 或签名载荷。两份交易即使调用效果相同，其 input、签名、hash 也可能不同。

如确实需要归一为共同布尔结论，必须先定义一个承载该结论的结果合同。当前目标仍是完整官方 transaction，因此不能以这种有损业务归类重写其字段，或返回某个代表源的原值并声称原值也被佐证。未定义这个出口前，不加入此类规则。

## 5. 主聚合范围与附属字段比较

字段是否属于官方定义，与是否命中归一规则，是两个独立判断：

| 内容 | 比较出口 | 对主裁决的影响 |
| --- | --- | --- |
| 官方 key，命中归一 | 规范值进入既有主比较 | 参与官方结果分簇、支持数及 Value 一致性检查 |
| 官方 key，未命中归一 | 官方定义范围内的原值进入既有主比较 | 同样参与主裁决，不因缺少规则而漏字段 |
| 外部／vendor key | 既有附属字段比较及来源证据 | 不影响 Core 分簇、门槛、Support／Reject 或成功结论 |

官方字段集合及嵌套结构来自固定 execution-apis bundle 和现有 schema／投影，不另写第二套名单。不把整个原始 JSON 当成主比较对象；新增外部 key、外部 key 缺失／null／值不同，都只能影响附属证据。

主比较仍保留官方内容的 JSON 类型、presence、数组顺序和精度。对象键序、空白与字符串 escape 按既有结构语义处理；未知语义不猜测。无归一规则不等于免验证，required、nullable、DATA／QUANTITY 和请求绑定检查照常执行。

### 复用已有 uncertain 与证据交付

用户所说的附属 uncertain 沿用已有机制，不新增 API，也不把原始证据快照改造成另一份受保护结果：

- 已有字段比较结果按现行规则进入 `Decision.Extensions`／`Decision.Diagnostics`：达到门槛的单一外部值才可成为 Extensions；其他观察保留为 Diagnostics，不升级为 Core。
- `CallResult.Uncertain` 是逐来源原始证据快照，`Fields` 保存未归一字段，`Raw` 保存来源响应／错误；它不是上述比较结论的别名。
- 继续遵守 `WithSourceEvidence`、既有交付条件、保留预算、截断标记及复制隔离；“随结果返回”不表示本轮改成默认无界保留全部响应。
- 外部字段一致不提高主结果保证，外部字段冲突也不否决已达到门槛的官方结果。附属证据不足不缩小冻结分母，不产生第二轮主投票。

这与现行 Core／Extensions／Diagnostics／source-evidence 隔离合同一致，不需要把决策 0050／0053 的域外隔离政策反转。仍复用现有附属比较的结构遍历；“不细化到更深维度”限制的是新增官方语义规则，不要求重写已有证据表示。

## 6. 比较范围、官方输出和 typed 不得混淆

主比较对象覆盖官方结果的保护域，**每个 Value 字段都必须被比较或由已验证内容可靠派生**。每个来源先完成修正、校验与规范编码，比较键和编码消费同一份候选对象；形成获胜簇后提交同源规范 Value，不从比较键重建回答，也不跨来源补齐。不只在 Normalize 改拼写，却让 Admit 或 Encode 消费另一份未经修正的值。

- 非官方字段不作为主比较 guard，也不进入官方 Value；比较结果与原始材料按既有附属证据合同交付。
- 一个来源缺官方 required 字段时，即使所有源都同样缺失，也不能产出“完整官方交易”；先裁决可否可靠派生，否则 Fault。
- 同簇 Value 必须一致。若相同分类得出不同官方输出，说明归一有损或 key 不充分；失败，而不是挑一份原文。
- 官方类型构造器用于形态约束与编码。原始响应须保留给既有证据流程；官方对象投影不能丢失应保护的官方内容，也不能把外部成员带回 Core。
- 官方掩码仅证明字段属于投影范围，不证明值合法。补回 typed 分支未承载的官方字段前，必须核对其适用约束已有验证 owner；不能以无条件合并或静默删除掩盖校验缺口。此覆盖核查是下一批任务，不据此预先声明当前所有分支已完整闭合。
- typed 必须来自最终受保护 Value。Geth 类型不能承载的官方信息仍留在 Value，不能只移入可关闭或可截断的原始证据通道，也不能默认成零值来伪装兼容。

**未知交易类型与未知字段不同。** 若未知类型导致当前实现无法验证 signed payload／hash 或无法交付约定的官方对象，它不具备主结果计票资格。可记录其原始材料与来源差异，但不得把 raw 字节一致升级为满足现有 transaction 保护合同的 Corroborated Value，也不新增“未知类型透传成功”旁路。typed 不支持而 wire 可验证的情况须另行裁决，不能把两种不支持混为一类。

## 7. 一条规则获准加入的条件

每条修正规则按**所属方法与官方 key**集中展示：原始形态、共同含义、完整规范值、适用条件、拒绝边界、样例及依据，以及对 hash／比较键／Value／typed 的影响。具名内部函数与相邻测试中的正反例形成项目内部语义积累，代码可直接查阅，不增加公开 API 或运行时配置语言。

不复制官方字段表，不进入 key 内部局部表示，不建立组合注册规则。没有差异就不造规则；无法证明完整 key 的安全修正就不兼容。不存在必须支持所有 RPC 方言的义务，不能以提高通过率为由降低官方输出标准。

规则必须确定、无来源偏好、与成员到达顺序无关，等价类应满足自反、对称、传递。不能因为收到第三种形态而改变前两种形态是否等价。同一 key 内规则重叠必须消除，不能靠分支顺序偷偷扩大接受域。

规则须有协议与可追溯样例依据。既有 codec 的表示等价前置可按规范及正反变异验证；provider 特有方言须保留真实原始响应，状态派生须实际验证相应状态，不能用合成数据冒充外部观察。入口及证据边界见 [测试执行路线](external-rpc-test-framework.md#当前执行路线先最小入口双线取证逐-key-收敛)。仓库 samples 仅提供原采集时刻事实；新的执行产物审核后才归档，未观察的外部形态继续保留缺口。

## 8. 执行依赖与对象出口

具体场景、工具落点和启动／清理／采集／归档入口统一见 [测试框架执行路线](external-rpc-test-framework.md#当前执行路线先最小入口双线取证逐-key-收敛)，本文不维护第二份框架施工单。

- 先冻结官方字段合同、目标交易与请求身份；真实 provider 定点采集、最小 Anvil 状态验证分别执行，不等完整框架或所有样本齐全。
- 官方 key 的形态规则依据本次真实观察逐项裁决；涉及 pending／mined 或状态关系时，同时验证对应 Anvil 场景。纯 required 修复依据规范先实施，不必等真实 provider 返回坏值，也不因此关闭外部观察缺口。
- 在现有方法装配与 transaction owner 内闭合安全解析 → 方法级 key 修正 → 官方与方法校验 → 逐源 EncodeCheck／比较 → Decision → typed。外部 key 经既有附属流程交付，缺 required 字段的反例不得继续穿过。
- by-hash 与 by-block/index 各自拥有修正入口及请求绑定；仅将确实相同的规则提取共享，不能强制两方法采用同一接受域。本地状态、provider 方言和离线回放分别报告证据，HTTP golden 与 raw WSS 边界不变。
- 同步验证不规范回答的来源归因及健康调度隔离；不得把未知形态、合法异见、标准能力错误和传输失败混为一种故障。
- 独立 transaction 完整切片验收后再接 block 内嵌交易；真实历史 fork、moving tags、EIP-1898 和订阅专项按实际依赖推进，不作为开始归一化的全局门。

不以 samples 数量、文档长度、架构变化或测试数量验收。分别证明：官方形态、key 语义等价边界、官方字段不漏、域外差异不干扰主裁决、固定分母和 typed 保真。未覆盖交易类型与状态仍是缺口，不能把首个场景完成叫作完整 transaction 或完整兼容。

## 9. 针对性验证与真实证据边界

主线是**官方字段在真实外部 RPC 中各异形态的收敛**。证据前置是可执行的采集／状态验证流程，不是本地 samples 目录：先按框架计划执行本次真实 provider 请求及必要的 Anvil 场景，再审核、冻结、接入离线回放。官方向量用于合同 oracle，现有 samples 仅补历史回归；二者均不制造本次外部观察事实，也不取代实际状态验证。不建设新的比较框架，不承担整个响应的保护。

采集与归类要求：

- 优先同网络、同固定历史交易、同方法请求的多 provider 回答，先确认比较的是同一对象，而不是不同交易或不同区块状态。
- 每份记录保留采集时刻、来源身份、请求、原始响应与完整性摘要；官方 key 的差异清单是派生材料，不能覆盖原始响应。外部字段比较另列，沿用既有附属承载边界。
- mined／pending、不同交易类型用于挑选有代表性的样本和验证官方分支，不进入运行时语义规则的复合注册 key。pending 状态本会变化，不能把跨时刻状态变化误记为等价拼写。
- 官方字段归类为“同 key 下等价形态”“真实语义异见”“待判定形态”；这是样本评审记录，不是新增聚合维度。未归一的合法官方形态继续原有比较，不自动放宽规则；非官方字段不因此加入主规则。
- RPC 错误、限流、超时、对象不存在与成功对象的形态分开记录，不把失败响应当成可归一的 transaction 字段。

验收场景应包括：

- 已审核的多种拼写聚到同一规范输出；相同支持集合下，输入顺序不改变 Value。
- 两份都 shape-valid、但官方 signed payload／上下文或业务声明不同的回答不得误合并。
- 缺官方 required、已知非法官方字段、请求绑定失败，即使来源一致也拒绝；失败来源仍占冻结分母。
- 官方字段相同而外部 key 新增／缺失／null／值不同，主比较键、支持集合、业务结论及 Value 不变；附属比较与来源证据能保留这些区别。
- 官方对象／数组中的额外非官方成员只进入附属流程；合法官方内容不同仍影响主比较，不生成深层语义规则。
- 数字不得因 float64 舍入被误合并；官方内容按相应 codec 保真，附属原始材料在已有保留预算内不被改写成归一值。证据未启用或截断也不能改变主结果。
- 错误输入不能因为规则返回 false 而绕过验证；未知类型不能获得已验证交易的成功保证。
- 合法但没有特殊修正规则的完整 key 继续参与比较；无法读取或校验失败的回答不计票。不得靠 key 内部局部修正或忽略字段提高通过率。
- 原始表示按已审核接受域检查，规范输出经独立官方 oracle 检查；不能要求获准兼容的原始拼写也通过严格输出 schema。方法语义另测，pending 等生成链修正提供独立依据，不能改本地 schema 后自证正确。
- 明确合同违规能追溯到来源并触发实施目标中的健康调度隔离；合法异见与附属差异不触发该归因。隔离、重试探测或恢复都不改变固定成员、分母和门槛。
- 公开 Root／EthClient 的 HTTP／raw WSS 路径验证最终值、错误、证据和 typed 保真；不只测匹配函数返回 true／false。

本轮已实现下述检查点，并实际运行固定 Anvil 的 HTTP／raw WSS 生命周期验收；未进行新公网 provider 采集。Anvil 的成功不能替代市场方言证据，旧样本仍只提供原采集时刻的外部事实。

## 10. 淘汰、健康隔离与停止兼容

本设计把有明确依据的接受域扩展与严格官方输出结合起来，比只机械接受规范拼写覆盖更多输入；它不保证兼容所有 provider，也不以牺牲标准、真实性或可验证性换可用性。

- 经正确官方合同和有限 key 规则仍不能读取或验证的回答，视为不满足项目方法合同，Fault、不计票，并保留来源与拒绝原因；到此停止兼容。不能把同样非法的多个回答投成成功。
- 合法语义异见不是不规范，不因少数意见处罚健康；外部字段差异也不处罚主结果。生成链自身错误须先修正，不能误记为 provider 违规。
- 复用现有来源健康与调度 owner，不新增健康算法、权重或黑名单框架。仅记录 Fault 不等于健康已更新；当前批次在既有 collection／emission 协作点接入最终记账，验收“明确合同违规 → 来源归因 → 既有调度隔离”。一次实际尝试只记一次，不能先以传输成功重置，再重复记合同失败而使持续违规无法触发门控；最终结果以第 12 节记录为准。
- 持续违规来源按现有隔离及探测节奏持续被阻止业务调度；这不是永久删除成员，也不承诺永不探测。遵守决策 0062：健康只门控调度，不能删除、替换成员或缩小任何一轮分母、降低门槛。彻底移除来源仍需重建 Client／调整构建时来源集合。
- 合法的 `-32601` 等方法能力错误不是畸形成功对象，不据此新增全局健康惩罚；长期能力不匹配沿用构建时来源选择。不能把“无法兼容回答”扩大为“任何失败都判 RPC 不规范”。

## 11. 开工顺序与完成出口

方案无需新增全局设计评审即可开工；具体规则的审核、生成正确性和实际证据属于实现任务，不是重建框架的理由。

1. 核对首个 transaction 场景的官方预期和生成链，优先修复 required／nullable 与保真基础路径；pending 按方法及完整上下文验证，不用填零让错误生成物通过。
2. 在两个方法文件接入静态集中 key 修正，复用已有安全检查、codec 和对象校验；确保比较与 Encode 读取同一来源同一候选对象。
3. 以可追溯样例逐条补正反例，验证无法兼容时拒绝及来源健康归因，不为了让更多来源通过继续细化 key。
4. 按 [测试执行路线](external-rpc-test-framework.md#当前执行路线先最小入口双线取证逐-key-收敛) 分别执行 provider 采集、必要 Anvil 场景和公开路径回放；复用工作树已有入口，不等完整平台、全部样本或所有交易类型齐全。
5. 交付完整官方 Value、typed 保真、既有附属结果以及固定分母和隔离行为的端到端证据，再推进内嵌 transaction；未覆盖类型与状态明确列出，不夸大为完整兼容。

## 12. 当前实施检查点

首个实施批次只闭合两个独立 transaction 方法及其必要基础路径，按用户要求收束供实现形态与规则审阅。后续双 switch 收敛及迁移方向已确认，推进方向见 [路线图](project-roadmap.md)；整理已有成果不表示继续推进内嵌交易、其他方法或新 provider 方言。

版本保存边界：2026-11-11 的文档提交仅保存设计与验收记录，当时实现仍在工作区。2026-11-12 已将既有 Go 实现、测试、生成物、采集入口及 fixture 按责任分批提交，源码归档基线为 `a5d9993`；具体提交及本轮验证见下文。不能把早先文档提交的 SHA 当作包含实现的源码身份。双 switch 收敛已于 2026-11-12 实施（见 13.1），其余方法的自治迁移亦已完成；transaction 之外的未覆盖兼容规则按各对象族 owner 文档另行裁决。

### 规则与实现入口

- `rpc/methods/transaction_by_hash.go` 的 `correctTransactionByHashKey` 是 by-hash 静态集中规则入口；`transaction_in_block.go` 自治保留 mined-only 检查及请求绑定。两者不互调准备／修正函数，只共享 transaction 对象能力及 wire codec，没有第二份注册表或规则引擎。
- `hash`／`blockHash`、`from`／`to` 与 `input` 复用既有完整 hash、地址、DATA codec；只能改等价表示，不能改变字节内容，null 仍由各字段及完整上下文约束决定。其他 key 保留完整值进入既有规范编码及校验。
- `blockTimestamp` 的唯一状态派生规则：仅当该 key 缺失，且同源 `blockHash`、`blockNumber`、`transactionIndex` 三项都明确存在且为 null，才派生完整值 null。任何位置字段缺失、非 null、混合状态，或 mined 缺 timestamp，都不补值。原始响应和 signed payload 不改。实际本地 Anvil 样例及原始请求／响应摘要保存在 `rpc/methods/testdata/transaction/anvil-1.7.1-pending.json`，该记录不是外部 provider 观察。
- `rpc/internal/objects/transaction/encode.go` 使用生成的独立 query 构造器检查完整 required／nullable／交易分支；保留既有官方 mask 选中的完整内容，不能因生成结构体不承载某个已识别 key 就静默删掉。`transaction.go` 继续验证 payload hash、签名、请求及上下文，并核对 protected legacy 交易声明的 chainId 与签名派生值，不做 sender recovery。
- `wire.PruneMethod` 使用精确数字解析并保留官方 null、missing 区别及数组位置；vendor 内容仍被官方投影隔离。pending 的生成链修正限于独立 query AST，原始官方 bundle 和内嵌 block 约束未改，详见 [wire-codegen](wire-codegen.md)。
- 健康接线通过既有 pool collection／emission 和 aggregator 结束点协作；默认 pool 调用与写入保持即时分类，不新增 Root／EthClient 配置。准入或编码明确违规才覆盖成功响应的健康结果，合法异见、标准错误、取消和未尝试按既有合同处理。

### 已执行证据与未完成范围

- 官方向量覆盖 type 0／1／2／3／4 的两个方法准入及一致规范输出；生成行为、精度／presence、固定分母、原始证据和错误绑定有离线回归。
- 公开 Root／EthClient 的本地 HTTP／raw WSS 正反例已执行，包括相同非法回答拒绝、pending 与 mined 的不同接受域、官方 Value 和附属证据隔离。
- 实际执行固定 Anvil 1.7.1 生命周期，单个本地来源上的 HTTP／raw WSS 与 Root／EthClient 共 88 条消费断言通过；覆盖 pending → mined、snapshot/revert 后可见性核对及重启恢复隔离。成功完整运行记录位于 `/tmp/ethguard-anvil-controller-03/transaction-lifecycle.json`，两个 transport 不是两个独立 provider。
- 主控执行生成器到临时文件并逐字比较，通过；公开 facade、integration、离线 replay、官方 conformance、投影和 transaction 专项通过。健康最终记账及调度有专项和 race 回归，不规范回答与合法异见分开归因。
- `go test ./...` 仍非全绿：仅剩开工基线已复现的 `TestMethodContractsForbiddenClaims` 和 `TestRuntimeEvidenceDocsForbiddenClaims` 两项文档互锁失败，分别要求 method-contracts 的测试链接和 roadmap 的固定措辞。本批次未删除测试或扩展修复；它们不能被解释为运行时回归，也不能据此宣称完整发布门禁已通过。
- 新公网采集、其他交易类型的真实状态生命周期、内嵌交易接入和全方法兼容尚未完成。本地测试通过不表示所有 RPC 方言都已支持；后续按路线图与实际证据缺口分批推进。

上述测试结果是实施检查点的日期化记录。后续文档整理补齐 method-contracts 的既有测试引用及 roadmap 的证据定义；文档门禁是否通过以该次实际检查为准，不改写这里的历史运行结果，也不据此宣称全库测试已通过。

### 迁移前归档基线（2026-11-12）

既有实现分批归档为：`fa08c12`（SyncProgress 保真）、`53ca5ce`（wire 保真与 receipt 合同）、`7d9d0de`（校验后健康记账）、`3da2742`（独立 transaction 规范化、定点生成修正和组织样板）、`1a229af`（采集工具）、`a5d9993`（固定 Anvil 验证入口）。这次保存没有开始新的方法迁移或双 switch 行为调整。

- 本轮完整源码的 `go test ./...`、`go test -race ./... -count=1` 与 `go vet ./...` 通过。此前两项文档互锁缺口已补齐并实际复核，不再作为当前已知失败保留；完整发布流程与公网观察仍未执行。
- 分批提交前从暂存区导出隔离副本验证。receipt 批次中混入的 transaction 新行为测试仅在 index 中拆分，完整工作文件不变；对应集成复核通过。transaction 暂存快照全库测试、健康批次 race、SyncProgress 与采集工具专项均通过。
- 生成器输出至临时文件后与已提交 `types.gen.go` 逐字一致；官方原始 bundle 未更改。Go 格式和 diff 空白检查通过。
- 从 Anvil 批次暂存快照实际执行 `go test -tags=anvil ./internal/verification/anvil -run '^TestTransactionLifecycle$' -count=1 -v`，固定 Anvil 1.7.1 的 88 条消费断言通过。单个本地来源覆盖 HTTP／raw WSS、Root／EthClient、pending → mined、snapshot/revert 后可见性与重启隔离；两种 transport 仍不算独立 provider。
- 本次本地运行产物为 `/tmp/ethguard-baseline-20261112-anvil-01/transaction-lifecycle.json`，仅作日期化运行记录；可重跑入口与最小 pending 原始样例已纳入源码。未新增公网 provider 捕获，临时施工计划及第三方原文不纳入 Git，也不成为正式文档依赖。

## 13. transaction 两方法的目录组织

按 [topology](codebase-topology.md#方法自治组织) 只对两个方法做自治组织：名称、公开接口映射、现有 MethodSpec、私有准备及 key 修正都在本方法文件；`Specs()`／catalog 对两项仅调用定义函数。共享完整对象校验与编码仍在对象层，不通过另一个正式方法文件复用。

原 `eth_gettransactionbyhash.go`／`eth_gettransactionbyblockhashandindex.go` 分别归并为 `transaction_by_hash.go`／`transaction_in_block.go`。私有符号用 gopls 语义重命名更新引用，公开常量名称和方法集合不变。临时 review 命名的回归测试归并为同名方法测试；跨方法编码检查保留独立测试文件。该组织调整与既有实现已于 2026-11-12 经用户授权分批提交；随后其余方法按同一布局完成迁移（见 method-transport-coverage 的读注册归属），未借归档新增兼容规则或公共配置。

已确认的扩散前调整包括 Encode 命名及校验覆盖核查、规范化职责收敛、timestamp 单次处理和外层 key／内层状态结构；in-block 当前无额外兼容规则，不强行复制 by-hash。上述调整均已实施并按 13.1 的合同测试钉住。

### 13.1 扩散前调整落地（P1／P2，2026-11-12）

Encode 命名收敛为 `officialFields`／`canonicalFields` 并注明合并方向（typed 规范值优先，仅补回官方投影保留而 typed 未表达的字段）；校验覆盖核查结论：transaction 官方分支是开放对象（无 `additionalProperties:false`），分支外成员属官方容忍的附加项，值无官方约束，经补回进入规范值；accessList 条目等官方闭子对象的未声明成员被掩码剔除，不进比较键也不进规范值。两项合同以 `encode_merge_contract_test.go` 钉住。评审中曾试行的"闭对象运行时拒绝"与既有 vendor 附属通道及"差异不自动处罚健康"设计冲突，未采纳，生成器未更改。

两方法已完成外层 key／内层状态收敛：状态判定只读原始响应，修正写入独立候选；`blockTimestamp` 缺失派生是唯一缺失派生 key，且仅在三个位置字段都明确为 null 时补完整 null，其余上下文不修正、交回官方必填校验与 typed 分类顺序（unsupported 类型优先级保持）；幂等与重复执行由 `TestTransactionByHashCorrectionIdempotence` 钉住。in-block 明确无额外兼容规则，未复制 by-hash 派生。

## 14. 字段处置与转换归属审计（2026-11-13）

当时审计输入是锁定 bundle `v1.0.0-beta.7` 的 `eth_getTransactionByHash` 结果 schema、生成器（当时含 `allowPendingTransactionQueryContext` 固定修正，语义指纹钉住）与本仓库生成结构；本节只记录事实、验证位置与例外，不建立第二份生产字段表。全部适用分支（type 0x0–0x4 与 null）都核对过，不是只用一份 type=2 fixture 代替。

2026-11-14 归属更新：上述 AST 修正已迁入 `internal/spec/errata/transaction-query-pending-context.json`，旧函数及生成器方法特判已删除。`internal/executionapi.Read` 先应用审核过的标准勘误，生成器、mask 和字段投影共用有效 schema；`ReadOriginal` 保留原始来源。四字段范围、required、非 null 约束、内嵌交易规则和下述方法检查保持不变，生成业务代码除审计头注释外逐字节一致。当前生效结构与退出条件见 [生成合同](wire-codegen.md#显式标准勘误与外部审计)及决策 [0070](../decisions.md#0070-explicit-schema-errata-before-generic-generation-and-runtime-schema-consumers)；本次迁移不改写以下历史运行证据。

### 14.1 分支与字段处置（官方声明 → 校验位置）

外层信封分支「Transaction information」声明六个 REQUIRED 成员：`hash`、`from`、`blockHash`、`blockNumber`、`blockTimestamp`、`transactionIndex`；显式标准勘误把其中四个包含上下文字段改为 `null ∪ string`（前三个位置字段依据 EIP-1474，`blockTimestamp` 额外依据 geth v1.17.10 RPCTransaction 行为），四字段仍全部 required，整体上下文一致性（三字段同 null 或同非 null，及缺失派生）由对象合并守卫与方法准入拥有：`encodeCanonical` 的 inconsistent pending/mined 守卫（见证 `TestTransactionInclusionContextGuardPerAsymmetricMember`）、by-hash 的唯一缺失派生（`TestTransactionByHashPendingTimestampRequiresExplicitPendingState`）、in-block 的 mined-only 拒绝（`TestTransactionInBlockPendingRejectionMessage`）。`hash`/`from` 无 null 容忍，缺席或非法即拒绝（`TestTransactionMissingSignatureFieldsReject`）。

五个类型分支的 REQUIRED/optional 事实（从锁定 bundle 提取）：

| 分支 | 独有 REQUIRED | 其他分支可容忍成员 | 分支内 optional |
| --- | --- | --- | --- |
| 7702 (0x4) | `authorizationList`；`gasPrice` 声明为 optional | `blobVersionedHashes`、`maxFeePerBlobGas`（来自 4844） | `gasPrice`、`v` |
| 4844 (0x3) | `blobVersionedHashes`、`maxFeePerBlobGas`；`gasPrice` optional | `authorizationList`（来自 7702） | `gasPrice`、`v` |
| 1559 (0x2) | `gasPrice` REQUIRED | 其余四分支的独有成员 | `to`（null 容忍，合约创建） |
| 2930 (0x1) | 无 `maxFeePerGas`/`maxPriorityFeePerGas` | 同上 | `to`、`v` |
| Legacy (0x0) | `v` REQUIRED、`chainId` optional | 同上 | `chainId`、`to` |

分支选择由 `type` 值的 pattern 钉住（每个分支约束自己的 type 字面量），所以 type=0x2 响应携带 4844 独有成员（`blobVersionedHashes`、`maxFeePerBlobGas`）不会翻分支：它们是官方容忍的跨分支附加项（见证 `TestEncodeTypePinsTheBranchAcrossForeignMembers`）。该容忍不得顺手拒绝或删除——类型 2 上 `maxFeePerBlobGas` 的既有行为由 `TestEncodePreservesOfficiallyToleratedMembers` 独立钉住。

### 14.2 未声明成员与闭子对象

- 官方分支均为开放对象（无 `additionalProperties:false`）。分支未声明、但官方 union mask（oneOf 各分支 properties 并集，`wire` 的掩码树）保留的成员，比较键与规范值都保留，值本身无官方约束；闭子对象（accessList 条目）的未声明成员被掩码剔除，不进比较键也不进规范值，剔除不产生任何 fault 或健康处罚（`TestEncodeMasksClosedObjectForeignMembers`、vendor 附属差异走 Diagnostics 不进 Core）。
- 附属证据归属：剔除只发生在比较键与规范值两层；原始响应按既有只读生命周期完整保留（见证 `TestTransactionCanonicalRoutePendingKeepsOriginalEvidence`）。

### 14.3 转换归属链（每规则一个事实 owner）

| 规则 | Owner | 见证 |
| --- | --- | --- |
| 方法上下文派生（缺失 timestamp 补全 null） | by-hash 方法文件 `correctTransactionByHashKey` | `TestTransactionByHashPendingTimestampRequiresExplicitPendingState` |
| codec 语法（hex 规范化） | `wire.PruneMethod` 内 `CanonicalizeHex` | `rpc/internal/wire` prune 测试 |
| 官方字段投影（union mask 白名单） | `wire.PruneMethod`（bundle 编译掩码） | 同上 |
| 标准定义勘误（四个 required inclusion 值允许 null） | `internal/spec/errata/transaction-query-pending-context.json`／`executionapi.Read` | `TestErrataOnlyChangesFourRequiredQueryValues`、来源漂移及原子失败测试 |
| 适用有效 schema 验证（required／nullable／分支解析） | 生成结构 `New<Branch>Transaction` / `NewGetTransactionByHashResult` | `TestTransactionMethodsAcceptOfficialPayloadTypes`、`wire` 生成测试 |
| 对象声明／payload 一致性（hash/payload、签名、chainId、包含上下文语法） | `objects/transaction.validatePrepared` | `TestCandidateBindingsMatchCompatEntries` 及对象包全部测试 |
| 规范输出（typed 优先 + 官方补回） | `encodeCanonical` 合并 | `TestEncodePreservesOfficiallyToleratedMembers` |
| 请求绑定 | `Candidate.AdmitByHash` / `AdmitInBlock` | `TestTransactionProcessorBindingSemanticsUnchanged` |
| typed 公共投影 | `internal/typed.DecodeTransaction` | facade typed 测试 |

`PruneMethod` 目前仍包含 hex 规范化——本轮未全局改写其行为；该职责重叠的后续收敛仍须按拓扑文档检查实际实现。

### 14.4 审计结论

本轮未发现真实字段漏验反例：每个官方分支的 required 成员都在生成结构解析中强制，信封字段的一致性由合并守卫与准入拥有，签名／sender／payload 绑定在 `validatePrepared` 一次完成。对「unprotected legacy（非 EIP-155）分支的 chainId 声明不匹配」路径：当前 `validatePrepared` 只在 `tx.Protected()` 时校验声明 chainId，非保护 legacy 的声明值是开放对象的容忍成员——与官方 schema 的 `chainId: optional` 一致，不构成接受域漏洞；若未来需要更强的 legacy chainId 语义，须按第 7 节的条件另行评审。审计产生的唯一新见证是跨分支容忍成员的分支钉住（14.2/14.1）；没有新语义规则进入代码。
