# Wire codegen：pinned execution-apis 结果类型

## 目标与边界

`ethapi/types.gen.go` 由固定版本的官方 execution-apis 请求／结果 schema 生成，不依赖 go-ethereum 方言类型。结果输入是 `internal/spec/methods.json` 与同版本的 `simulatev1/simulate.json`，请求输入为 `internal/spec/requests.json`；来源身份和各 bundle 摘要固定于 `internal/spec/SOURCE.json`。原始 bundle 不改；经用户确认的标准勘误保存在 `internal/spec/errata/*.json`，由共享读取入口应用后形成有效 schema。它是明确的项目勘误，不冒称上游原样定义。

当前输出器已恢复为 Jennifer v1.7.1：类型、函数、值约束、数组与 union 均由结构化 API 生成，使用 `File.Save` 输出，不保留字符串源码 emitter。此次迁移保留既有 schema 建模、精确数字、nullable、分支和规范字节语义。历史上 `01be3dd` 于 2026-11-11 引入 Jennifer，紧接的 `7618d29` 在修复语义时撤换为字符串输出器；提交说明没有解释撤换必要性，不能据此判断授权或动机。恢复输出器不等于整体回滚该提交。

生成定义现已实际迁入公开包 `ethapi`，没有使用 internal 类型 alias。共用数值／JSON 编码原语位于 `internal/ethcodec`，内部 wire 保留 mask、裁剪和规范编码。生成器、mask 与官方字段 projection 已统一使用 `internal/executionapi.Read`，同版本的独立 Simulate bundle 也已可见。Simulate native 入口消费生成 payload 与 selector；生成类型与逐方法迁移状态见 [路线图](project-roadmap.md)。

生成层负责形态、required、值约束、数组、分支与 nullable 的解析和规范编码。`New*` 入口执行这些检查；直接手工构造 Go 值时，调用者必须保持同样的不变量。Marshal 不通过整树 JSON 重解析重复准入，union 额外检查选中成员数量及 nullable 状态；新请求的 extras 另检查 JSON 安全和声明 key 碰撞。方法注册、请求绑定、签名/区块承诺、receipt bloom 一致性、受保护 Value 字段投影仍属于对象/方法层。独立 transaction 已在方法修正后接入生成 query 构造器，仍保留 Geth 载荷及请求校验；生成类型不替代内容与信任检查。

生成类型表达固定官方合同，不是容纳所有 provider 字段的万能响应 struct。[前置生态兼容](method-contracts.md#前置生态兼容与官方语义检查) 由方法拥有，经审核的规则先形成候选，生成层随后执行适用的官方检查；未命中兼容规则的官方值仍须接受这些检查。不得因遇到新方言而向生成类型不断加入厂商字段，或以宽松类型替代无法表达的官方约束。域外内容通过 [附属及原始证据通道](return-value-design.md#数据交付与责任范围) 交付，Go 类型投影不承担统一市场上所有响应形态的责任。

### 已迁移的公开调用族

- `CallContract`／`CallContractAtHash`／`PendingCallContract` 和三个 `EstimateGas` 入口接受 `ethapi.CallMsg`，字段直接来自原始 `eth_call.params[0]`，不再使用 geth CallMsg 转换。三个官方调用 schema 的一致性由来源测试锁定。入口重新验证直接构造的值，返回 `ErrInvalidRequest`；缺失、显式 null、零值和空数组不互相折叠。
- 当前固定来源为 CallMsg 新增 optional `commitments`／`proofs` DATA 数组。它们经同一生成检查与参数冻结交付，不另建手写字段白名单；请求携带这些数据不表示 EthGuard 验证了其密码学关系。
- `FeeHistory` 的 root/view 返回 `*ethapi.FeeHistoryResult`，保留 optional blob 字段和精确 `Number`。`CreateAccessList` 返回 `*ethapi.AccessListResult`，字段的 optional presence 不再由手写 DTO 抹除；其请求入口仍待迁移。
- `SyncProgress` 返回 `*ethapi.SyncProgress`，仅有官方三个 optional 字段。geth 的额外同步／索引计数不进入官方主值，可由 Diagnostics／Extensions 和有预算的原始来源证据读取。`Done()` 不再是该生成类型的方法；七个旧场景保留完整原始来源，并独立检查剩余计数，不能将缺失计数解释为完成。
- 外部 Go 代码须适配以上名义类型及字段形状；geth bind 接口的签名断言已由逐方法完整参数／结果签名合同替代。交易、receipt、block、log、selector、写入和订阅业务类型仍待迁移，算法检查继续保留。

`requests.json` 原样保存 49 个官方 eth_／net_version 的参数描述，属于来源清单，不是已执行的方法注册表，也不表示已生成全部请求类型。当前消费 CallMsg 及 Simulate 的两个参数；其他请求不能从原样库存推导为已生成或已检查。

### Simulate 请求模型与构造策略

`SimulateV1Request`、`BlockStateCall`、`SimulateCall`、`BlockOverrides`、`AccountOverride`、`StateOverrides`、`AccountStorage` 和 `SimulateBlockReference` 由同一有效 schema 生成。嵌套 withdrawal／access-list 使用请求专用模型，避免与既有结果投影的接受策略错误共享。`SimulateCall` 不强行复用结构与约束并不等价的 CallMsg。

新 Simulate 请求采用 `strictRequests` 构造策略，模式参与模型去重；既有结果投影和 CallMsg 保留原策略。这里的 strict 表示忠实检查请求 schema，不表示所有对象均封闭：

- 官方 `additionalProperties:false` 的固定对象拒绝未知 key；封闭同型 `patternProperties` 字典要求每个 key 匹配规则并检查对应值。地址／storage-key 字典的 key 保留原始大小写，不合并不同拼写。
- 官方开放的固定对象通过生成的 `AdditionalProperties map[string]json.RawMessage` 保留未声明输入。编码时不得覆盖任何已声明 key，即使该 typed 字段缺失；未知字段不经 float64，也不因字段名仅大小写相似而同时填入另一个 typed 字段。这是请求保真，不改变响应 Value／Extensions 的职责。
- 公开请求构造器在根执行 JSON 安全检查，随后调用生成的私有子 parser，不为每个子字段重复整树准入。独立调用子模型的公开构造器同样有根检查；extras 编码拒绝不安全原文和声明字段碰撞。

通用生成能力包括 string／bool／精确 number 的标量 enum、对象 `not:{required:[...]}`、封闭同型 pattern 字典，以及数组 minItems／maxItems／递归精确 uniqueItems。唯一性上下文递归保留字符串的原始语义值，避免把大小写不同的合法地址或其他不同字符串规范化后误判重复；数字仍按精确 JSON 数值比较。结果投影遇到可能丢未知成员、从而破坏唯一性的开放对象组合时明确生成失败，不改写既有结果投影政策。其他 not 形态、对象／数组 enum、异型或开放 pattern 字典、尚未实现的组合附加约束均在生成期报错，不能静默丢失约束。能力来自通用建模和 Jennifer 输出，不按 RPC 修改 schema AST，也不引入运行时 schema 解释器。

固定来源的 base 有五个 anyOf 分支：quantity、tag、裸 hash、仅含 quantity blockNumber 的封闭对象、含 blockHash 与 optional requireCanonical 的封闭对象。对象 blockNumber 不接受 moving tag；省略／nil 默认 latest 是方法行为，不把 null 加入官方 union。保留官方分支顺序，前导零及全零 32-byte hash 必须往返不变，不能被当作数量缩短。

95 个官方 payload 与 base 均有独立往返验证；其中 31 个顶层 RPC 错误的请求也合法，不能当作 schema 拒绝样例。unknown-account／block-override-key 官方请求仍按开放 schema 保留输入，后续方法／上游错误语义不由生成层改写。

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

外部审核可直接比对原始 bundle、勘误文件和生成文件头部；运行 `go test ./internal/executionapi -run '^TestErrata' -v` 可查看实际 input／patch／output 摘要及失败边界。当前固定 commit 为 `981aa66d79364be8322a9c358aaf0a571cca0873`，pending query 勘误经复核续用；Simulate items 与 getLogs 空数组各有独立勘误。没有对上游已修问题叠加本地补丁。

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

`eth_getLogs` 的原始 schema 使用 hashes/logs 两个数组分支的 oneOf，`[]` 会同时匹配两者。`getlogs-empty-array-overlap.json` 只将该方法 result 的 oneOf 改为 anyOf，保留当前公开双分支模型和非空分支约束，使空结果合法；不修改原始 bundle 或通用 oneOf 规则。生成层能解析 hash 数组，不代表受保护方法接受该分支；生产方法仍只接受 logs。非法混合数组、null 和非数组不因该修正成为合法结果。

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

### SimulateV1 官方来源与接受域

`internal/spec/simulatev1/SOURCE.json` 固定 commit `981aa66d79364be8322a9c358aaf0a571cca0873`、原始 bundle 摘要与 95 个官方 `.io` 的逐文件摘要。来源从官方源码及未修改的 specgen 重建，不依赖临时目录，也不把官方向量标作 provider 观察。

`types_simulate_vectors_test.go` 对 95 个 request/response 对逐一记账：64 个 result 进入原始 schema 和生成解析器，检查声明字段、presence 和编码幂等；31 个顶层错误独立记账，不进入 result parser。这是结果模型证据，不替代原生方法、顶层错误分类或 provider 执行验证。

上游已取消非 revert VM error message 的旧 pattern，`out of gas` 不再与该字段约束冲突；旧版两个冲突的测试侧消息替换已退出。新版同时要求 VM error 的 code 和 message 均存在，生成字段从 optional 改为必填，`{}` 不再是该分支合法值。取消这一字段的官方 pattern 不授权全局放宽其他响应约束。

部分失败 call 携带的 logs 和 error.data 仍不属于相应官方分支的声明字段；不能直接合入主值。已有原生预算原文保留用例，完整分支与预算矩阵仍须逐方法验收，不能从模型往返测试推导为已完成交付。

### 固定来源与显式勘误

固定来源 [`981aa66d79364be8322a9c358aaf0a571cca0873`](https://github.com/ethereum/execution-apis/tree/981aa66d79364be8322a9c358aaf0a571cca0873) 已包含 blobVersionedHashes 数组、prevRandao bytes32、balance-only overrides、VM 消息和 Payload 对象类型的官方修复。仍存在的来源缺口独立处理：

- `transaction-query-pending-context.json`：仅为独立 transaction query 的四个 inclusion 字段保留 required-but-nullable；不影响完整区块内嵌交易。
- `simulate-block-state-calls-items.json`：将 array 上原有 block execution properties 原样移入 object items；不添加客户端数量限制、默认值或时间规则。生成的强类型请求消费修正后的结构，方法语义检查保持独立。
- `getlogs-empty-array-overlap.json`：只解除该方法空数组的 oneOf 重叠，不改变方法的 logs-only 保护政策。

该来源新增官方 block/Simulate slotNumber，byte pattern 与 net_version 十进制范围也有变化。业务保护域与生成接受域分别审阅；完整区块仍按既有 header 哈希及内容承诺检查。HexByte 保留既有兼容规范化（大小写转 canonical、前导零折叠），再以新 pattern 检查规范值；原始 schema conformance 仍检查原始 wire 的 lowercase/non-empty 限制。`net_version` 生成结果现允许十进制零，不能从中推导 NetworkID 的逐方法接受域已全部校正。时间默认和递增关系留在方法语义，不塞入 schema 勘误。

## 修改与迁移流程

1. 从固定官方 commit 重建原始 schema；同步 VERSION、SOURCE 与所有 bundle，核查每份 errata 的失效／退出条件，不根据 provider 观测改写上游原件。
2. 修改生成模型/策略，明确新增形态的支持边界；不支持则报错。
3. `go generate ./ethapi`，连同生成文件审阅与提交。
4. 运行生成器、wire、conformance 及受影响对象测试；CI 检查生成新鲜度。
5. 同步本文及 `internal/spec/README.md`，不保留与现实现矛盾的覆盖声明。

生产接入按方法独立推进：在对象/方法契约内替换数据载体，验证规范字节、受保护字段域和承诺检查不退化，再删除对应旧路径。不能把一次 root parse/re-encode 当作完整方法迁移。
