# Wire codegen：pinned execution-apis 结果类型

## 目标与边界

`rpc/internal/wire/types.gen.go` 由固定版本的官方 execution-apis result schema 生成，不依赖 go-ethereum 方言类型。输入是 `internal/spec/methods.json` 与同版本的 `simulatev1/simulate.json`；不修改官方 schema 来适配现有实现。

生成层负责结果形态、required、值约束、数组、分支与 nullable 的解析和规范编码。`New*` 入口执行这些检查；直接手工构造 Go 值时，调用者必须保持同样的不变量。Marshal 不通过整树 JSON 重解析重复准入，union 只额外检查选中成员数量及 nullable 状态。方法注册、请求绑定、签名/区块承诺、receipt bloom 一致性、受保护 Value 字段投影仍属于对象/方法层。生成类型不是这些准入与信任判断的替代品；本轮未迁移生产方法。

## 生成管线

```text
官方 dereferenced result schema + 固定生成策略
    → 有序 schema 解码与支持边界检查
    → 带值约束/nullability 的模型及语义去重
    → 类型化值解析器、对象/union、方法结果入口
    → go/format → types.gen.go
```

- `gen/schema.go`：保留属性顺序、composition kind 和分支位置；未知验证关键字或无法完整表达的形态必须报错，不生成宽松替代。
- `gen/model.go` / `walk.go`：值持有类型、const/pattern/bounds、nullable、元素与分支语义；字段持有名称与 required/presence。命名策略与字段字节顺序不改变官方字段集合。
- `gen/emit.go`：消费经过检查的模型，字段、数组项、union 分支与 root 使用同一套值解析规则。optional 仅决定是否解析，不能跳过出现值的约束。
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

nullable root 的 null 表示 nil。数组空值 `[]` 与 nullable root 的 `null` 保持不同。receipt 的 `to:null` / `contractAddress:null` 保留在官方类型里；现有受保护 encoder 省略它们的政策留在外层。

### Composition 与分支

模型保留真实的 oneOf/anyOf 及原始分支身份；每个分支必须执行其全部支持的类型和值约束。分支不能仅因映射到同一 Go 类型而被合并。

oneOf 要求恰好一个分支匹配；anyOf 要求至少一个匹配，并按声明顺序选择规范表示。生成层与离线 conformance 都保留这一区别。`BlockTransactions` 为 anyOf，空数组可合法匹配多个分支。新增无法可靠表达的组合须生成期拒绝，不能丢失约束后选择首个成功分支。

`eth_getLogs` 的生成结果保留官方 hashes/logs 两个数组分支；只允许 logs 的受保护方法政策不再裁剪生成根类型。该 pinned schema 使用 oneOf，而 `[]` 同时匹配两个数组分支，因此严格生成入口拒绝 `[]`。这是 pinned 定义与既有项目 getLogs 空结果政策的差异，生产方法本轮不切换，不能以改 schema 或静默放宽 oneOf 隐藏它。

## 命名、版本与新鲜度

path override、title override 和稳定回退仅决定 Go 名称，不得改变标准语义。路径保留 composition kind 与原始 index；不保留旧合成路径兼容别名。语义 fingerprint 排除 title/description，但包含约束、nullable、composition 与子值语义。

生成时要求两个 bundle 的版本与 `spec.Version()` 一致；生成文件在包初始化时检查版本。这是生成期检查和运行时 init guard，不是编译期校验，也不可能由测试函数抢先于 init 执行。

CI 将生成结果写入临时文件并与已提交产物比较，防止同版本 schema/生成器修改后忘记重生成。版本字面量测试、源码片段或函数数量测试不能代替这一门禁。

## 验证层次

1. **官方 schema conformance**：固定版本 `.io` request/response 对按官方 result schema 离线验证。
2. **生成行为**：官方成功结果经对应生成入口解析、编码；验证标准字段内容保留、输出 schema、再次解析编码的幂等和 nullable 语义。
3. **受保护字节 parity**：logs、receipt、accessList、feeHistory 与现有 encoder 比较。每侧分别必须成功，不允许双方拒绝后当作成功。receipt 仅在 parity 比较中显式应用既有 nullable-null 投影；未投影的生成输出另外受官方形态与内容保留检查。
4. **独立负例**：optional const/pattern、错误 JSON 类型、非 nullable null、HexByte 语法、官方 Number 的精确边界、项目 Ratio 的精度/尾随、unsupported schema。预期不由另一套共享实现决定。

`.io` 只使用 `conformance.ParseVector` 的一份解析器：按出现顺序配对每条 request/response，身份为完整路径与交互序号，结构化匹配 method，明确区分 error、null、缺失 result。它不是按 JSON-RPC ID 重排的网络路由器。

block/transaction 的生成向量 round-trip 与 protected parity 分开；生产方法仍保留原有 geth/对象路径。simulate bundle 没有 pinned 官方 `.io`，使用独立正反 fixture 与输出 schema 检验，不能宣称官方向量覆盖。

## 修改与迁移流程

1. 从固定 release 重建官方 schema；同步更新 VERSION 与所有 bundle，不根据 provider 观测改写定义。
2. 修改生成模型/策略，明确新增形态的支持边界；不支持则报错。
3. `go generate ./rpc/internal/wire`，连同生成文件审阅与提交。
4. 运行生成器、wire、conformance 及受影响对象测试；CI 检查生成新鲜度。
5. 同步本文及 `internal/spec/README.md`，不保留与现实现矛盾的覆盖声明。

生产接入按方法独立推进：在对象/方法契约内替换数据载体，验证规范字节、受保护字段域和承诺检查不退化，再删除对应旧路径。不能把一次 root parse/re-encode 当作完整方法迁移。
