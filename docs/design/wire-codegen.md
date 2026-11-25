# 官方来源与类型生成合同

状态：2026-11-26 生成原则与前置增强修复已通过独立验收，结果和成本见 [路线图](project-roadmap.md)。公开业务类型与调用方迁移已完成；内部对象是否仍维护平行模型独立审查。本文件拥有来源、勘误、通用生成、presence／数值／分支、命名和新鲜度，不定义方法绑定、密码学承诺或多源保证。简短阅读入口为 [ethapi/README.md](../../ethapi/README.md)。

## 1. 唯一来源与职责

原始请求库存 internal/spec/requests.json、结果 bundle methods.json、VERSION、SOURCE.json 和官方原始向量共同固定来源。当前结果覆盖26项（含 SimulateV1 与两个 Header 合同），请求库存49项不代表全部已生成或注册。`executionapi.ReadOriginal` 提供独立原始视图，`Read` 应用明确审核的勘误后形成生成器、mask、projection 共同消费的有效 schema。

`ethapi/types.gen.go` 与 `types.audit.json` 是可再生产物，不是修正入口；不手改生成文件、不在调用方补平行 DTO。生成层负责形态、required、nullable、标量与数组约束和分支。方法拥有有限兼容、请求绑定与业务关系；私有对象内核保留签名、hash、bloom、内容承诺。runtime 不解释 schema，生成模型不承担投票或来源信任。

两层设计为“前置增强 + 官方兜底”：前置层显式修复固定标准的冲突或缺失，记录依据和退出条件；通用生成器忠实执行增强后的有效 schema，未增强的部分按固定官方定义执行。运行时 provider 兼容处理实际输入，标准增强处理定义，两者分别归属，不能在构造器或 codec 中隐藏修补。阅读前置处理、生成原则和官方定义应足以解释生成产物。

## 2. 统一变更流程

```text
原始官方 schema → 来源审计 → 显式前置修复／补充
  → executionapi.Read 有效 schema → 通用生成原则／有序模型／语义去重
  → Jennifer 结构化输出 → ethapi 类型与 audit → 消费者与独立行为验收
```

生成缺口先区分官方无定义、已有定义错误或不完整、尚未选择生成、生成器不支持约束。前两类需要有依据的前置修复／补充；覆盖选择改根清单，翻译能力修通用模型／输出器。当前 errata 要求目标 schema 已存在，不提供整项缺失定义的注册机制；没有具体消费者需求时不新增该机制。`net_listening`／`net_peerCount` 缺官方条目，仍按项目方法合同维护，不冒称已由官方 schema 生成。

大小写、QUANTITY、DATA 等表示政策需分别审计输入接受域和规范输出。构造器先检查解码后的原始值，再处理合法值的表示；运行时 provider 兼容先形成候选再调用构造器，不能反向修改固定来源。

Jennifer v1.7.1 使用结构化 API 与 File.Save，不保留字符串源码 emitter。gen/schema.go 保留属性顺序、composition 与原始分支，model.go／walk.go 表达约束与 presence，emit.go 共用值解析器。未知或无法忠实表达的约束在生成期失败，不产宽松替代；不引入运行时 schema 解释器或按 RPC 改 AST 的特判。

当前公开模型覆盖 Address／Hash、Block／Header／Transaction／Receipt／Log、CallMsg、FilterQuery、TransactionReceiptsQuery、FeeHistory／AccessList／SyncProgress 与 Simulate。请求结构和同名结果投影不因形似就共用接受策略。CreateAccessList 的 any 请求入口是现存公开扩展，不是尚未完成的 geth 类型迁移；完整请求生成库存仍按方法需要扩充。

## 3. 请求构造与生成能力

`SimulateV1Request`、`BlockStateCall`、`SimulateCall`、`BlockOverrides`、`AccountOverride`、`StateOverrides`、`AccountStorage` 和 `SimulateBlockReference` 由同一有效 schema 生成。嵌套 withdrawal／access-list 使用请求专用模型，避免与既有结果投影的接受策略错误共享。`SimulateCall` 不强行复用结构与约束并不等价的 CallMsg。

已选择的所有请求根（CallMsg、BlockReference、FilterQuery、收据订阅子模型及Simulate的payload／base）统一采用 `strictRequests` 构造策略，模式参与模型去重；开放结果使用声明字段投影，封闭结果同样拒绝未知成员。所有对象按精确 JSON key 取值，不借用 Go struct 解码的大小写宽容匹配。选择清单只决定生成覆盖，不包含RPC业务语义。这里的strict表示忠实检查请求schema，不表示所有对象均封闭：

- 官方 `additionalProperties:false` 的固定对象拒绝未知 key；封闭同型 `patternProperties` 字典要求每个 key 匹配规则并检查对应值。地址／storage-key 字典的 key 保留原始大小写，不合并不同拼写。
- 官方开放的固定对象通过生成的 `AdditionalProperties map[string]json.RawMessage` 保留未声明输入。编码时不得覆盖任何已声明 key，即使该 typed 字段缺失；未知字段不经 float64，也不因字段名仅大小写相似而同时填入另一个 typed 字段。这是请求保真，不改变响应 Value／Extensions 的职责。
- 公开请求构造器在根执行 JSON 安全检查，随后调用生成的私有子 parser，不为每个子字段重复整树准入。独立调用子模型的公开构造器同样有根检查；extras 编码拒绝不安全原文和声明字段碰撞。

通用生成能力包括 string／bool／精确 number 的标量 enum、对象 `not:{required:[...]}`、封闭同型 pattern 字典，以及数组 minItems／maxItems／递归精确 uniqueItems。唯一性上下文递归保留字符串的原始语义值，避免把大小写不同的合法地址或其他不同字符串规范化后误判重复；数字仍按精确 JSON 数值比较。结果投影遇到可能丢未知成员、从而破坏唯一性的开放对象组合时明确生成失败，不改写既有结果投影政策。其他 not 形态、对象／数组 enum、异型或开放 pattern 字典、尚未实现的组合附加约束均在生成期报错，不能静默丢失约束。能力来自通用建模和 Jennifer 输出，不按 RPC 修改 schema AST，也不引入运行时 schema 解释器。

固定来源的 base 有五个 anyOf 分支：quantity、tag、裸 hash、仅含 quantity blockNumber 的封闭对象、含 blockHash 与 optional requireCanonical 的封闭对象。对象 blockNumber 不接受 moving tag；省略／nil 默认 latest 是方法行为，不把 null 加入官方 union。保留官方分支顺序，前导零及全零 32-byte hash 必须往返不变，不能被当作数量缩短。

95 个官方 payload 与 base 均有独立往返验证；其中 31 个顶层 RPC 错误的请求也合法，不能当作 schema 拒绝样例。unknown-account／block-override-key 官方请求仍按开放 schema 保留输入，后续方法／上游错误语义不由生成层改写。

## 显式标准勘误与外部审计

| 层 | 修改对象与时机 | 归属 |
| --- | --- | --- |
| 标准勘误（Spec Errata） | 构建／初始化固定参考数据时，对特定请求或结果 schema 应用审核过的 JSON Patch | `internal/spec/errata/*.json` 与 `internal/executionapi` |
| 前置生态兼容 | 实际请求或来源响应，按已知字段及同源上下文形成候选 | 公开入口、各方法及共享对象 owner |
| 通用生成器 | 忠实翻译有效 schema，支持标准关键字 | Jennifer generator，不包含方法语义修正 |
| 附属及原始证据 | 原始来源字段、差异和预算内材料 | Extensions／Diagnostics／source-evidence，不改写官方主值 |

每份勘误单独声明 ID、上游版本和 commit、目标 method 及 result／request parameter（从 0 起）、目标 schema 的输入／输出 SHA-256、理由、HTTPS 依据、退出条件和 [RFC 6902](https://www.rfc-editor.org/rfc/rfc6902) patch。JSON Pointer 仅在审核过的 schema 数据中使用，不形成响应字段路径规则系统。应用器使用固定 `json-patch/v5 v5.9.11`，关闭非标准负索引、隐式创建祖先和忽略缺失 remove；没有从 provider／请求／公开选项加载 patch 的入口。

输入摘要覆盖该勘误应用前的实际 schema 字节：首项来自固定 bundle，后续项来自有序前置勘误的输出。输出摘要覆盖固定 JSON Patch 实现的实际输出；格式／拼写变化也会触发复核，不把字节摘要冒称语义 fingerprint。勘误按文件名排序，后续输入摘要同时固定其前置修复身份；整批失败不会交付部分修正的数据。

`executionapi.ReadOriginal()` 保留上游原始视图，独立 conformance／升级审核继续以它或原始 bundle 为 oracle。`executionapi.Read()` 原子地应用全部勘误，供生成器、mask 和字段投影共用；任何勘误失败都不交付半成品。它同时返回实际应用文件的 ID 和摘要，生成源码头部记录这些身份，CI／release 新鲜度门可识别勘误变化。

版本／commit／输入摘要变化、patch 前置 test 失败、重复 ID／重复应用、没有实际改变 schema 文档、错误输出摘要或不完整审计记录均失败。上游升级后必须复核、删除或更新勘误，不能自动忽略失败，也不自动替维护者改写 patch。原始 bundle、官方向量及其来源摘要始终保持独立。

独立 query 勘误仅给 blockHash／blockNumber／blockTimestamp／transactionIndex 的值增加 null 分支，四项仍 required，原非 null 约束保留；前三项和额外 timestamp 的依据分别记录在勘误中。不修改完整块内嵌交易合同，不自动派生缺失字段；pending 上下文一致性与 by-hash 的有限派生由方法拥有。旧生成器内 pending 特判已退出。

## 官方形态与项目规范字节

有效 schema 定义字段、可选分支和输入接受域；representation policy 定义已通过检查的值如何表示，两者不得混为一谈。Go 名称与表示政策的映射可人工维护，但数值模型的增减和值域必须由 schema 的 type/pattern/minimum/maximum/const 驱动，不能由映射表另造约束。

- 字符串只解码一次，先按有效 schema 检查原始字符串，再应用显式表示政策。Quantity、Hash、DATA、Bloom、BlockNonce、StorageKey、DecimalString 已有严格拼写，直接保留合法值；不再调用宽容 codec 后检查规范输出。DATA 保留空值、字节长度及前导零，完整字节限制来自显式 DATA 勘误。
- Address 的官方 pattern 允许混合大小写 hex digits、要求小写 `0x` 前缀；通过后可输出小写。HexByte 仅接受 `0x` 后 1–2 个小写 hex 字符，通过后折合一个冗余前导零：`0x04` → `0x4`、`0x00` → `0x0`；`0x4A`、`0X4a`、符号及三个 hex 字符均拒绝。
- string const／enum 以及递归 uniqueItems 上下文保留原字符串语义值；即使 pattern 对应 Address，也不先改大小写而破坏字面常量、枚举或唯一性。普通字符串的 JSON 转义不属于语义值，编码可采用统一形式。
- 真正多非null的oneOf／anyOf也递归保留已验证字符串：转换不能使原本唯一匹配的输入在输出后命中另一分支。私有parser传递 `keepStrings`，保持Address／HexByte等公开类型；单个nullable包装只传递已有上下文。独立子模型构造器从无组合上下文开始，其合法表示可能与经组合根构造不同，两者都须满足相应schema。
- 官方 Number 只接受完整 JSON number，精确保留任意十进制值，不经 float64；minimum/maximum 来自各 schema 的真实边界，由精确比较执行。`0.30000000000000001` 不因 float64 精度损失被拒绝。负零规范为 `0`，科学计数法与 fixed 输出的选择只影响拼写，不限制数值域；不会展开巨大指数。
- 数字 const 按精确 JSON 数值比较。即使其 Go 表示为 int，`3`、`3.0`、`3e0` 也都匹配常量 3，再赋值为 3；字符串 `"3"` 和精确不等的十进制数不匹配。
- `CanonicalRatio` 仍是项目受保护 feeHistory encoder 的额外规范化政策：`[0,1]` 内按 Go 最短 round-trip 输出，并拒绝改变精确十进制原值的输入。官方生成 Number 不调用它；两者不能共用一个“是否合法”的结论。
- Quantity256 上限为 64 个 hex digits，即 32 bytes；Bloom 为 512 个 hex digits，即 256 bytes。
- 固定字符串 pattern 约束输入；当前 net_version 的官方模式允许十进制 `"0"`，拒绝 `"000"`、`"+1"` 和负数；对象／方法层的接受政策另列，不能称为官方限制。
- 字段顺序采用确定的文档顺序，已有受保护编码器金标的对象族保留显式顺序表。紧凑 JSON、无 HTML 转义。
- 封闭结果拒绝未声明字段；开放结果只投影声明字段。运行时消费者需接收 provider 扩展时，由明确的前置 owner 提取官方字段，原始扩展继续留在来源证据中，不以标准构造通过代表扩展已验证。

现有兼容由入口明确选择字段再调用 `ethcodec.FoldHexSpelling`：它只折合单个已知 hex JSON 字符串的前缀及字母大小写，保留长度、前导零；非法值原样交给后置检查。Header／Receipt 依据生成模型的字段标签提取官方字段，Syncing 的共享 owner 供方法和 typed 入口复用。AccessList 的 error、Simulate 的 error.message 等自由文本不应用该操作；不建立任意对象或未知字段的递归修正规则。

Block body、直接Transaction及Simulate结果的已有兼容由 `internal/ethobject/hexcompat` 共用：仅为显式生成模型建立有限已知字段树，折合白名单hex类型，保留Text／Number及未知内容。AccessListItem／Withdrawal／Log的前置字段投影明确登记；不接受外部规则或运行时schema。HexByte另执行合法值的最短表示，交易按明确type选择字段树，防止将typed交易的v规则误套给legacy的Quantity。原始来源保持不变，字段与内容检查仍由后置owner承担。

公开Filter入口保留其已承诺的大小写兼容，按固定字段及数组层级处理冻结快照；调用方额外成员保持。CallMsg、Simulate原始请求及直接标准构造器遵守有效schema，不因旧构造器曾隐含接受大写或非规范十进制而扩张前置规则。直接Go赋值后Marshal仍不等于通过构造检查。

### 缺失与 null

required 与 nullable 是两个独立维度。非 nullable 值拒绝 JSON null；nullable 值保留 null；optional nullable 字段区分缺失与显式 null，不以省略制造相等。

nullable root 的 null 表示 nil。数组空值 `[]` 与 nullable root 的 `null` 保持不同。receipt 的 `to:null` / `contractAddress:null` 在官方类型和受保护 encoder 中均保留；缺失、null 与地址值参与不同的比较，见决策 0068。

### Composition 与分支

模型保留真实的 oneOf/anyOf 及原始分支身份；每个分支必须执行其全部支持的类型和值约束。分支不能仅因映射到同一 Go 类型而被合并。

oneOf 要求恰好一个分支匹配，包括全部 null 匹配；anyOf 要求至少一个匹配，并按声明顺序选择规范表示。含显式 null 分支的 anyOf 将 JSON null 统一表示为空选择，即使更早的嵌套分支也接受 null；编码仍为 null，其余输入按首个成功分支表示。生成层与离线 conformance 都保留 oneOf／anyOf 的接受域区别。`BlockTransactions` 为 anyOf，空数组可合法匹配多个分支。

开放结果投影可能删除未知字段，从而让另一 oneOf 分支开始匹配，或让 anyOf 改选更早分支。`gen/projection.go` 在生成期检查这种变化：oneOf 的可投影分支须对全部竞争分支证明稳定，anyOf 须对更早分支证明稳定。证明只采用互斥 JSON 类型、双方必填且保留的不同精确标量 const／已支持的单值 hex pattern，或数组元素类型互斥；空数组投影不变。无法证明时以双方来源路径和分支序号报 unsupported，不生成隐含失真的模型。请求模式保留开放对象的额外成员，无需该投影证明；其他约束仍由普通 walker 检查。

固定来源中的交易 type、Simulate status／error code，以及 object／null、object／boolean 和 hash-array／object-array 组合满足上述证明。该检查不进入库的运行时，也不是通用 schema 求解器。新增组合若不能可靠表达，须扩展有独立反例的生成能力或明确拒绝，不能丢失约束后选择首个成功分支。

`eth_getLogs` 的原始 schema 使用 hashes/logs 两个数组分支的 oneOf，`[]` 会同时匹配两者。`getlogs-empty-array-overlap.json` 只将该方法 result 的 oneOf 改为 anyOf，保留当前公开双分支模型和非空分支约束，使空结果合法；不修改原始 bundle 或通用 oneOf 规则。生成层能解析 hash 数组，不代表受保护方法接受该分支；生产方法仍只接受 logs。非法混合数组、null 和非数组不因该修正成为合法结果。

## 命名、版本与新鲜度

path override、title override 和稳定回退仅决定 Go 名称，不得改变标准语义。路径保留 composition kind 与原始 index；不保留旧合成路径兼容别名。语义 fingerprint 排除 title/description，但包含约束、nullable、composition 与子值语义。

公开模型及其可达分支须固定语义名称，不能把遍历顺序触发的 `2` 等碰撞后缀作为长期接口命名政策。独立交易查询与区块内嵌交易因有效 nullable 合同不同，应分别命名、保持各自约束；去重不能抹掉这一区别。当前查询union及五分支已固定为 `Transaction`、`SetCodeTransaction`／`BlobTransaction`／`EIP1559Transaction`／`AccessListTransaction`／`LegacyTransaction`；内嵌区块使用 `BlockTransaction` 及 `Block` 前缀五分支。显式名称按完整语义fingerprint预登记，冲突直接失败，不以自动后缀掩盖。旧数字后缀名已替换，属于公开生成类型名变更。`SimulateBlockReference`保留为同源`BlockReference`的生成别名及构造器shim。

扩充请求生成覆盖时，根选择必须明确 method、参数序号及必要的 schema 子路径，保留完整来源定位和所选节点摘要；只生成所需子模型，不把有重叠分支的整个订阅参数 union 当作某一业务查询。当前requestRoot已支持RFC 6901 JSON Pointer；收据订阅选择`eth_subscribe.params[1]/oneOf/2`，固定所选紧凑schema摘要，路径／分支漂移失败。源路径、节点摘要、稳定名称与别名进入生成审计，并有独立验证；调用方不手工裁剪schema。

返回模型接入前还须核对该方法实际提交的 Value 能否满足目标有效 schema。生成模型已存在不代表当前受保护投影字段齐全；不得从代表源 Raw 补必填字段、制造零值或放宽生成校验来接通迁移。模型的结构检查、对象内部完整性、方法请求绑定与裁决信任分别由现有 owner 承担；共享对象检查须覆盖查询、订阅事件和同源 Pull，不能仅凭查询 admission 已接通便删除订阅 decoder 的校验。

生成时要求两个bundle的版本与 `spec.Version()` 一致；生成文件在包初始化时检查版本。这是生成期检查和运行时init guard，不是编译期校验，也不可能由测试函数抢先于init执行。

`ethapi/types.audit.json`由同一次生成产生：记录有效schema逐根摘要、errata身份、请求覆盖及子节点摘要、稳定模型名／别名、标量表示政策及具有标量表示或对象构造政策的源路径。全局标量条目用 `inputPattern` 记录输入约束，路径条目用 `sourcePattern` 记录来源约束，`representationPolicy` 记录通过后的表示处理；`normalizer` 仅记录实际调用的精确数 codec。该索引不重复数组、布尔、null及组合的完整约束，须与逐根摘要指向的有效schema一起阅读。路径记录不因模型去重而省略，const／enum／uniqueItems下保留原字符串、无type数值常量、对象精确key及封闭政策也明确记账。新增表示策略必须先有显式政策，普通生成亦拒绝未登记规则，不能仅在输出audit时才检查。该文件是构建审计材料，不是运行时registry，也不是官方schema的替代品。

CI和release门将生成Go及audit写入临时文件并分别与已提交产物比较，防止同版本schema／生成器修改后忘记重生成。audit包含生成Go摘要；新鲜度不能单独证明语义正确，版本字面量、源码片段或函数数量测试也不能代替独立行为验证。

## 6. 独立验证

1. 原始官方 schema conformance 使用未修改来源与原始 .io，和应用勘误后的有效合同分开报告。
2. 所有方法共用向量枚举；原始成功 result 经生成入口往返，核对声明内容、输出有效性、幂等及 presence。顶层错误不进 result parser，未覆盖错误 schema 明确列出。
3. 受保护 parity 与生成往返分开；每侧必须独立成功，双方拒绝不能算 parity 通过。Block／Transaction／Receipt 等的对象保护与规范 schema 分别验证。
4. 独立负例覆盖 const／pattern／bounds、optional 出现值、非 nullable null、精确数、分支重叠和不支持形态。现场生成／编译／执行 parser，不用另一套共享实现产生全部预期。

CallMsg 与 Simulate 请求根执行 JSON 安全检查后使用私有子 parser；直接调用子模型公开构造器也有根检查。直接构造 Go 值的使用者仍须满足不变量；入口按合同重新验证，Marshal 不重复整树解析来冒充准入。union 额外验证选中分支数量和 nullable 状态，开放请求 extras 编码验证原文安全及 key 碰撞。

直接结果构造器不统一承担 JSON 大小、深度和重复 key 准入；实际方法／对象消费入口按既有合同执行 `jsonsafe`。该安全边界和 schema 忠实性分别验证，不把本轮生成修复表述为扩大了所有公开构造器的安全保证。

## 7. 固定来源与向量

固定来源 [`981aa66d79364be8322a9c358aaf0a571cca0873`](https://github.com/ethereum/execution-apis/tree/981aa66d79364be8322a9c358aaf0a571cca0873) 已包含 blobVersionedHashes 数组、prevRandao bytes32、balance-only overrides、VM 消息和 Payload 对象类型的官方修复。仍存在的来源缺口独立处理：

- `transaction-query-pending-context.json`：仅为独立 transaction query 的四个 inclusion 字段保留 required-but-nullable；不影响完整区块内嵌交易。
- `simulate-block-state-calls-items.json`：将 array 上原有 block execution properties 原样移入 object items；不添加客户端数量限制、默认值或时间规则。生成的强类型请求消费修正后的结构，方法语义检查保持独立。
- `getlogs-empty-array-overlap.json`：只解除该方法空数组的 oneOf 重叠，不改变方法的 logs-only 保护政策。
- `wire-data-*.json`：依据 EIP-1474 的完整字节定义，将 DATA 的 `^0x[0-9a-f]*$` 显式修正为 `^0x([0-9a-f]{2})*$`。22 份勘误覆盖 13 个结果根、9 个请求参数根、64 个节点，应用于上述结构勘误之后；保留空 DATA 和前导零，不影响 StorageKey、HexByte 或 QUANTITY。原始 schema 仍保留其允许半字节的缺口，独立 conformance 不偷偷套用该修正。

该来源新增官方 block/Simulate slotNumber，byte pattern 与 net_version 十进制范围也有变化。业务保护域与生成接受域分别审阅；完整区块仍按既有 header 哈希及内容承诺检查。HexByte 先检查原始 lowercase/non-empty 约束，再折合合法前导零；已有消费者的大写拼写兼容在显式前置处理处保留。`net_version` 生成结果允许十进制零，不能从中推导 NetworkID 的逐方法接受域已全部校正。时间默认和递增关系留在方法语义，不塞入 schema 勘误。

## 8. 维护与实施阶段

来源升级先重建原始 bundle 和向量，核对 VERSION／SOURCE／摘要及每份勘误退出条件；再审查生成模型与消费者差异，生成、执行相关对象／方法／conformance 和新鲜度检查。具体可复现源命令见 [spec 数据说明](../../internal/spec/README.md)。

生成新鲜度只证明源与产物同步。公开签名完成不关闭内部平行模型、接受域或 provider 缺口。FeeHistory／AccessList／共享 Log 的候选已有部分实施、部分保留，当前状态和顺序由方法合同及路线图维护。后续若调整 schema、生成器或生成资产，仍遵循本文的来源审计与生成流程，不因结构整理静默改变生产接受策略。
