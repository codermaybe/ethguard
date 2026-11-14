# Wire codegen：pinned execution-apis 结果类型

## 目标与边界

`ethapi/types.gen.go` 由固定版本的官方 execution-apis result schema 生成，不依赖 go-ethereum 方言类型。结果输入是 `internal/spec/methods.json` 与同版本的 `simulatev1/simulate.json`，请求输入为 `internal/spec/requests.json`；来源身份和各 bundle 摘要固定于 `internal/spec/SOURCE.json`。原始 bundle 不改；经用户确认的标准勘误保存在 `internal/spec/errata/*.json`，由共享读取入口应用后形成有效 schema。它是明确的项目勘误，不冒称上游原样定义。

当前输出器已恢复为 Jennifer v1.7.1：类型、函数、值约束、数组与 union 均由结构化 API 生成，使用 `File.Save` 输出，不保留字符串源码 emitter。此次迁移保留既有 schema 建模、精确数字、nullable、分支和规范字节语义。历史上 `01be3dd` 于 2026-11-11 引入 Jennifer，紧接的 `7618d29` 在修复语义时撤换为字符串输出器；提交说明没有解释撤换必要性，不能据此判断授权或动机。恢复输出器不等于整体回滚该提交。

生成定义现已实际迁入公开包 `ethapi`，没有使用 internal 类型 alias。共用数值／JSON 编码原语位于 `internal/ethcodec`，内部 wire 保留 mask、裁剪和规范编码。生成器、mask 与官方字段 projection 已统一使用 `internal/executionapi.Read`，同版本的独立 Simulate bundle 也已可见；这不代表 Simulate 方法已经注册。整体迁移尚未完成，当前状态与接手顺序见 [路线图](project-roadmap.md)。

生成层负责结果形态、required、值约束、数组、分支与 nullable 的解析和规范编码。`New*` 入口执行这些检查；直接手工构造 Go 值时，调用者必须保持同样的不变量。Marshal 不通过整树 JSON 重解析重复准入，union 只额外检查选中成员数量及 nullable 状态。方法注册、请求绑定、签名/区块承诺、receipt bloom 一致性、受保护 Value 字段投影仍属于对象/方法层。独立 transaction 已在方法修正后接入生成 query 构造器，仍保留 Geth 载荷及请求校验；生成类型不替代内容与信任检查。

生成类型表达固定官方合同，不是容纳所有 provider 字段的万能响应 struct。[前置生态兼容](method-contracts.md#前置生态兼容与官方语义检查) 由方法拥有，经审核的规则先形成候选，生成层随后执行适用的官方检查；未命中兼容规则的官方值仍须接受这些检查。不得因遇到新方言而向生成类型不断加入厂商字段，或以宽松类型替代无法表达的官方约束。域外内容通过 [附属及原始证据通道](return-value-design.md#数据交付与责任范围) 交付，Go 类型投影不承担统一市场上所有响应形态的责任。

### 已迁移的公开调用族

- `CallContract`／`CallContractAtHash`／`PendingCallContract` 和三个 `EstimateGas` 入口接受 `ethapi.CallMsg`，字段直接来自原始 `eth_call.params[0]`，不再使用 geth CallMsg 转换。三个官方调用 schema 的一致性由来源测试锁定。入口重新验证直接构造的值，返回 `ErrInvalidRequest`；缺失、显式 null、零值和空数组不互相折叠。
- `FeeHistory` 的 root/view 返回 `*ethapi.FeeHistoryResult`，保留 optional blob 字段和精确 `Number`。`CreateAccessList` 返回 `*ethapi.AccessListResult`，字段的 optional presence 不再由手写 DTO 抹除；其请求入口仍待迁移。
- `SyncProgress` 返回 `*ethapi.SyncProgress`，仅有官方三个 optional 字段。geth 的额外同步／索引计数不进入官方主值，可由 Diagnostics／Extensions 和有预算的原始来源证据读取。`Done()` 不再是该生成类型的方法；七个旧场景保留完整原始来源，并独立检查剩余计数，不能将缺失计数解释为完成。
- 外部 Go 代码须适配以上名义类型及字段形状；geth bind 接口的签名断言已由逐方法完整参数／结果签名合同替代。交易、receipt、block、log、selector、写入和订阅业务类型仍待迁移，算法检查继续保留。

`requests.json` 原样保存 43 个官方 eth_／net_version 的参数描述，属于来源清单，不是已执行的方法注册表，也不表示已生成全部请求类型。现有公开请求模型只消费 CallMsg；未知 schema 关键字仍在生成期拒绝。

### 已确认的 transaction 接入边界

[方法级 key 兼容设计](transaction-semantic-matching.md) 已实施。方法文件中的静态 switch 只修正完整 key 的已审核输入形态，之后执行有效 schema 的约束与方法检查；生成器不收集 provider 方言，也不由运行时修正规则改写 schema。不增加 `MethodPolicy`、运行时响应的通用 Overlay 系统或第二份字段／方法表。下文标准勘误仅修正固定 schema，不对 provider 报文执行 JSON Patch。

生成链表达固定上游来源及显式审核的标准勘误。若约束在翻译时丢失，修通用生成模型；若上游定义表达不足，先核查官方修复，必要的本地差异以独立数据记录。2026-11-14 用户明确接受显式勘误层，取代此前“零本地语义补丁”的目标；仍禁止生成器按 RPC 修改 AST、静默放宽约束或扩张运行时 provider 兼容。

required 与 nullable 独立；pending／mined 的完整上下文关系仍由方法准入检查。不得给完整区块内嵌交易无条件继承独立查询的 pending 规则，也不得用零 hash／高度／索引伪造 inclusion。

### 显式标准勘误与外部审计

| 层 | 修改对象与时机 | 归属 |
| --- | --- | --- |
| 标准勘误（Spec Errata） | 构建／初始化固定参考数据时，对特定请求或结果 schema 应用审核过的 JSON Patch | `internal/spec/errata/*.json` 与 `internal/executionapi` |
| 前置生态兼容 | 每个来源的实际响应，按官方完整直接 key 及同源上下文形成候选 | 各方法的既有 switch |
| 通用生成器 | 忠实翻译有效 schema，支持标准关键字 | Jennifer generator，不包含方法语义修正 |
| 附属及原始证据 | 原始来源字段、差异和预算内材料 | Extensions／Diagnostics／source-evidence，不改写官方主值 |

每份勘误单独声明 ID、上游版本和 commit、目标 method 及 result／request parameter（从 0 起）、目标 schema 的输入／输出 SHA-256、理由、HTTPS 依据、退出条件和 [RFC 6902](https://www.rfc-editor.org/rfc/rfc6902) patch。JSON Pointer 仅在审核过的 schema 数据中使用，不形成响应字段路径规则系统。应用器使用固定 `json-patch/v5 v5.9.11`，关闭非标准负索引、隐式创建祖先和忽略缺失 remove；没有从 provider／请求／公开选项加载 patch 的入口。

输入摘要覆盖从固定 bundle 提取的 schema 原始字节，输出摘要覆盖固定 JSON Patch 实现的实际输出；格式／拼写变化也会触发复核，不把字节摘要冒称语义 fingerprint。

`executionapi.ReadOriginal()` 保留上游原始视图，独立 conformance／升级审核继续以它或原始 bundle 为 oracle。`executionapi.Read()` 原子地应用全部勘误，供生成器、mask 和字段投影共用；任何勘误失败都不交付半成品。它同时返回实际应用文件的 ID 和摘要，生成源码头部记录这些身份，CI／release 新鲜度门可识别勘误变化。

版本／commit／输入摘要变化、patch 前置 test 失败、重复 ID／重复应用、没有实际改变 schema 文档、错误输出摘要或不完整审计记录均失败。上游升级后必须复核、删除或更新勘误，不能自动忽略失败，也不自动替维护者改写 patch。原始 bundle、官方向量及其来源摘要始终保持独立。

### 已实施：pending query 勘误迁出生成器

`internal/spec/errata/transaction-query-pending-context.json` 只作用于 `eth_getTransactionByHash` result，为 `blockHash`、`blockNumber`、`blockTimestamp`、`transactionIndex` 增加 null 分支，保留 required 与原有非 null 约束。前三项的依据为 [EIP-1474](https://eips.ethereum.org/EIPS/eip-1474#eth_gettransactionbyhash)，时间戳额外依据为 [锁定 geth 的 RPCTransaction／newRPCTransaction](https://github.com/ethereum/go-ethereum/blob/v1.17.10/internal/ethapi/api.go)，不声称 EIP 已规定第四项。

旧 `gen/transaction_query.go` 及 `gen/main.go` 方法特判已删除。迁移后的生成代码除审核记录注释外逐字节一致，没有新增其他业务语义；两个 block 入口及 by-block/index 的原始约束保留。独立来源测试反向去掉四个 wrapper 后核对完整 schema 不变，并验证请求／其他结果／原始字节不变；生成模型与五类 pending／mined 行为回归继续验证上下文隔离。

外部审核可直接比对原始 bundle、勘误文件和生成文件头部；运行 `go test ./internal/executionapi -run '^TestErrata' -v` 可查看实际 input／patch／output 摘要及失败边界。该批保持 beta.7，先迁出已存在的 AST 债务；上游五项已修问题不新增 beta.7 补丁，下一步先升级官方基线，再处理仍缺失的 Simulate items 和 getLogs 空数组勘误。

## 生成管线

```text
官方原始请求／结果 schema + 独立审核的 Spec Errata
    → executionapi.Read 的统一有效 schema
    → 有序 schema 解码与支持边界检查
    → 带值约束/nullability 的模型及语义去重
    → 类型化值解析器、对象/union、方法结果入口
    → Jennifer 结构化生成与 File.Save → types.gen.go
```

- `gen/schema.go`：保留属性顺序、composition kind 和分支位置；未知验证关键字或无法完整表达的形态必须报错，不生成宽松替代。
- `gen/model.go` / `walk.go`：值持有类型、const/pattern/bounds、nullable、元素与分支语义；字段持有名称与 required/presence。命名策略与字段字节顺序不改变官方字段集合。
- `gen/emit.go`：通过 Jennifer 消费经过检查的模型，字段、数组项、union 分支与 root 使用同一套值解析规则。optional 仅决定是否解析，不能跳过出现值的约束。Jennifer 只属于生成工具依赖，不进入生成结果的运行时依赖。
- `gen/emit_test.go`：在临时模块现场生成、编译和执行解析器，独立验证精确常量／边界、optional 约束、缺失与 null、嵌套数组及分支重叠；同时核对相同输入的确定性输出。
- 不引入运行时 JSON Schema 解释器或插件 registry。生成期不能支持的结构直接返回错误；新增标准特性须先明确模型及行为验收。

## 官方形态与项目规范字节

官方 schema 定义字段与可选分支；canonical policy 定义不改变值的确定性字节形式，两者不得混为一谈。Go 名称与共享 normalizer 的映射可人工维护，但数值模型的增减和值域必须由 schema 的 type/pattern/minimum/maximum/const 驱动，不能由映射表另造约束。

- 数量、地址、hash、DATA 等复用 wire normalizer；规范输出必须匹配对应官方 pattern。DATA 保留字节长度及前导零，地址/hash 输出小写。
- HexByte 仅接受 `0x` 后 1–2 个 hex 字符，输出 canonical QUANTITY；`0x04` → `0x4`，`0x4A` → `0x4a`，不接受符号或三个 hex 字符。
- 官方 Number 只接受完整 JSON number，精确保留任意十进制值，不经 float64；minimum/maximum 来自各 schema 的真实边界，由精确比较执行。`0.30000000000000001` 不因 float64 精度损失被拒绝。负零规范为 `0`，科学计数法与 fixed 输出的选择只影响拼写，不限制数值域；不会展开巨大指数。
- `CanonicalRatio` 仍是项目受保护 feeHistory encoder 的额外规范化政策：`[0,1]` 内按 Go 最短 round-trip 输出，并拒绝改变精确十进制原值的输入。官方生成 Number 不调用它；两者不能共用一个“是否合法”的结论。
- Quantity256 上限为 64 个 hex digits，即 32 bytes；Bloom 为 512 个 hex digits，即 256 bytes。
- 固定官方字符串 pattern 仍约束规范输出；不能把 net_version 的负数或零包装为有效官方十进制字符串。
- 字段顺序采用确定的文档顺序，已有受保护编码器金标的对象族保留显式顺序表。紧凑 JSON、无 HTML 转义。
- 未声明的 provider 字段不进入标准类型；这属于类型投影，不代表已经验证来源的所有扩展字段。

### 缺失与 null

required 与 nullable 是两个独立维度。非 nullable 值拒绝 JSON null；nullable 值保留 null；optional nullable 字段区分缺失与显式 null，不以省略制造相等。

nullable root 的 null 表示 nil。数组空值 `[]` 与 nullable root 的 `null` 保持不同。receipt 的 `to:null` / `contractAddress:null` 在官方类型和受保护 encoder 中均保留；缺失、null 与地址值参与不同的比较，见决策 0068。

### Composition 与分支

模型保留真实的 oneOf/anyOf 及原始分支身份；每个分支必须执行其全部支持的类型和值约束。分支不能仅因映射到同一 Go 类型而被合并。

oneOf 要求恰好一个分支匹配；anyOf 要求至少一个匹配，并按声明顺序选择规范表示。生成层与离线 conformance 都保留这一区别。`BlockTransactions` 为 anyOf，空数组可合法匹配多个分支。新增无法可靠表达的组合须生成期拒绝，不能丢失约束后选择首个成功分支。

`eth_getLogs` 的生成结果保留官方 hashes/logs 两个数组分支；只允许 logs 的受保护方法政策不再裁剪生成根类型。该 pinned schema 使用 oneOf，而 `[]` 同时匹配两个数组分支，因此严格生成入口拒绝 `[]`。这是 pinned 定义与既有项目 getLogs 空结果政策的差异，生产方法本轮不切换。后续按已确认的顺序通过独立、显式的标准勘误消除重叠，保持原始 bundle 和通用 oneOf 语义，不静默掩盖此差异。

## 命名、版本与新鲜度

path override、title override 和稳定回退仅决定 Go 名称，不得改变标准语义。路径保留 composition kind 与原始 index；不保留旧合成路径兼容别名。语义 fingerprint 排除 title/description，但包含约束、nullable、composition 与子值语义。

生成时要求三个 bundle 的版本与 `spec.Version()` 一致；生成文件在包初始化时检查版本。这是生成期检查和运行时 init guard，不是编译期校验，也不可能由测试函数抢先于 init 执行。

CI 将生成结果写入临时文件并与已提交产物比较，防止同版本 schema/生成器修改后忘记重生成。版本字面量测试、源码片段或函数数量测试不能代替这一门禁。

## 验证层次

1. **官方 schema conformance**：固定版本 `.io` request/response 对按官方 result schema 离线验证。
2. **生成行为**：官方成功结果经对应生成入口解析、编码；验证标准字段内容保留、输出 schema、再次解析编码的幂等和 nullable 语义。
3. **受保护字节 parity**：logs、receipt、accessList、feeHistory 与现有 encoder 比较。每侧分别必须成功，不允许双方拒绝后当作成功。receipt 仅在 parity 比较中显式应用既有 nullable-null 投影；未投影的生成输出另外受官方形态与内容保留检查。
4. **独立负例**：optional const/pattern、错误 JSON 类型、非 nullable null、HexByte 语法、官方 Number 的精确边界、项目 Ratio 的精度/尾随、unsupported schema。预期不由另一套共享实现决定。

`.io` 只使用 `conformance.ParseVector` 的一份解析器：按出现顺序配对每条 request/response，身份为完整路径与交互序号，结构化匹配 method，明确区分 error、null、缺失 result。它不是按 JSON-RPC ID 重排的网络路由器。

block/transaction 的生成向量 round-trip 与 protected parity 分开；独立 transaction 已组合使用生成 query 结构和既有 Geth/对象准入，完整 block 未随之迁移。Simulate 官方向量与冲突按下节单独验收，不能并入“全部通过”的泛化声明。

### SimulateV1 官方来源、向量与待解决冲突

`internal/spec/simulatev1/SOURCE.json` 固定上游 `v1.0.0-beta.7` 对应 commit `5aebdfdd45cadeb723be4bd45b4611b71c8b1c85`、bundle SHA-256 与 91 个原始 `.io` 的逐文件 SHA-256。导入时核对了上游 Git blob 身份；官方未修改的 specgen 重建后，Simulate bundle 与仓内逐字节一致，methods bundle 的 JSON 语义一致。此前“上游没有 Simulate 官方向量”的记录错误，已纠正；当前数据与测试不再依赖临时目录。

`types_simulate_vectors_test.go` 对所有 91 个 request/response 对逐一记账：27 个顶层错误不进入 result parser；62 个 result 通过原始 schema 和生成解析器，并核对全部已声明字段、presence 和规范编码幂等；另 2 个 result 明确保留为上游冲突，不算成功。这只是生成结果层证据，不是原生方法、顶层错误分类或 provider 执行证据。

两个冲突为 `ethSimulate-run-out-of-gas-in-block-38015.io` 和 `ethSimulate-use-as-many-features-as-possible.io`：原始 VM 错误消息 `out of gas` 不符合固定 schema 的 `^vm execution error.*`。测试要求原文在已知位置拒绝，并仅在测试副本中替换该消息后独立验证其余字段成功；生产解析器、官方 schema 和原始向量均不修补。若官方约束变化，必须复核并退出对应冲突记录。

部分官方样例还在失败 call 上携带 `logs` 和 `error.data`，而失败分支 schema 未声明这些字段。测试始终解析原始响应，按明确的已声明字段投影比较；不因原文含有该材料就把它加入官方业务类型。未来原生方法仍须通过 Extensions／原始证据通道交付这些附加材料，不能从结果类型测试推导出已完成交付。

请求侧仍需解决 `blockStateCalls` 的 array／缺失 items、`blobVersionedHashes` 的 string 声明、balance-only 与 state/stateDiff 分支，以及 time 说明等来源冲突。它们和已有 pending transaction 修正分别登记，不能用更换输出器掩盖。

### 2026-11-14 上游修复核查

对官方主干固定 commit [`981aa66d79364be8322a9c358aaf0a571cca0873`](https://github.com/ethereum/execution-apis/tree/981aa66d79364be8322a9c358aaf0a571cca0873) 的核查发现：blobVersionedHashes 数组（#940）、prevRandao bytes32（#941）、balance-only overrides（#942）、VM 消息（#948）和 Payload 对象类型（#896）已有官方修复。但该 commit 的 [`src/schemas/execute.yaml`](https://github.com/ethereum/execution-apis/blob/981aa66d79364be8322a9c358aaf0a571cca0873/src/schemas/execute.yaml) 仍将 BlockStateCalls 声明为带 properties、没有 items 的 array；[`src/schemas/transaction.yaml`](https://github.com/ethereum/execution-apis/blob/981aa66d79364be8322a9c358aaf0a571cca0873/src/schemas/transaction.yaml) 仍未将 inclusion context 声明为 pending nullable。仅升级到这个来源不足以实现零勘误的完整业务类型。

用户已接受显式标准勘误层；本批已迁出 pending AST 修正，保持原有接受域。生产 pin 仍为 beta.7；先升级官方基线，再对仍存在的问题添加独立勘误。不能把原始定义、有效 schema 与运行时兼容规则混称同一来源。

## 修改与迁移流程

1. 从固定官方 commit 重建原始 schema；同步 VERSION、SOURCE 与所有 bundle，核查每份 errata 的失效／退出条件，不根据 provider 观测改写上游原件。
2. 修改生成模型/策略，明确新增形态的支持边界；不支持则报错。
3. `go generate ./ethapi`，连同生成文件审阅与提交。
4. 运行生成器、wire、conformance 及受影响对象测试；CI 检查生成新鲜度。
5. 同步本文及 `internal/spec/README.md`，不保留与现实现矛盾的覆盖声明。

生产接入按方法独立推进：在对象/方法契约内替换数据载体，验证规范字节、受保护字段域和承诺检查不退化，再删除对应旧路径。不能把一次 root parse/re-encode 当作完整方法迁移。
