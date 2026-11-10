# Wire 层 codegen：execution-apis 类型层生成

> 状态：已落地（分支 `feat/execution-apis-wire-codegen`）。前置：
> `method-spec-reference-freeze.md`（版本钉住）、`return-value-design.md`（canonical
> 字节序与 §5 byte 型字段规则）、`go-ethereum-compatibility-policy.md`（go-ethereum
> 方言类型的退出路径）。

## 1. 目标与不变量

`rpc/internal/wire` 是项目的 result 类型层。本设计把这一层从「手写 + go-ethereum
方言类型」切换为「由 execution-apis 官方 schema 生成的本地类型」，并以四条不变量
钉住它：

1. **字节级 parity**。生成层与受保护的手写编码器（log / receipt / accessList /
   feeHistory）对同一官方 `.io` vector 的输出逐字节一致。字节流是比较键
   （return-value-design.md §2），因此这是一致性的硬门禁，不是风格选择。
2. **零 go-ethereum import**。生成代码只依赖 stdlib 与本包手写的校验器
   （`wire.CanonicalQuantity`、`ValidateWireFixedHex`、`CanonicalRatio` 等）。
   标准定义全部落成 `type X string` / 本地 struct / tagged union wrapper。
3. **版本 interlock**。`types.gen.go` 内嵌与 `internal/spec.Version()`
   （v1.0.0-beta.7）一致的 bundle 版本，`github.com/codermaybe/ethguard/rpc/internal/wire`
   侧 panic-on-mismatch。生成器、`methods.json`、`simulatev1/simulate.json` 三者
   同步升级，缺一步都无法编译 Regen 后通过。
4. **失败面不降级**。官方 schema 中无法得体表现的 feature（未知 key、过深嵌套
   union、数组套数组的 union 分支）在生成期 panic，而不是把宽松校验偷渡进运行时。

## 2. 管线

```
internal/spec/methods.json (官方 beta.7 dereferenced bundle)
internal/spec/simulatev1/simulate.json (eth_simulateV1 单方法 bundle, PR 扩展)
        └── rpc/internal/wire/gen/  (go run ./gen -out types.gen.go 或者 cd rpc/internal/wire && go generate ./...)
             schema.go  有序 schema 解码（保留官方文档属性序，容忍未知 key）
             model.go   命名/pattern 映射/override 表
             walk.go    递归建模：required、const/enum 约束、ref 去重、null 提升
             emit.go    jennifer 发射 + go/format
        └── rpc/internal/wire/types.gen.go (生成: DO NOT EDIT)
```

`go:generate go run ./gen -out types.gen.go` 标记在 `rpc/internal/wire/generate.go`；
从 `rpc/internal/wire/` 执行（`-out` 相对 cwd）。生成器与代码均为仓内包
（`github.com/codermaybe/ethguard/rpc/internal/wire/gen`），零运行时依赖
jennifer —— 生成只发生在工程的构建协商之外。

## 3. 类型映射

### 3.1 pattern → 原生类型

官方 schema 每个 string 属性的 `pattern` 决定 Go 原生类型。映射表（`nativeByPattern`，
见 `gen/model.go`）覆盖官方 beta.7 已知的全部 pattern；映射后生成 `NewX` 解析 +
`MarshalJSON`（no HTML escape，canonical 拼写）。

| pattern 族 | 原生类型 | 校验 |
| --- | --- | --- |
| `^0x(0\|[1-9a-f]...*)$`（宽松 quantity） | `Quantity` | `wire.CanonicalQuantity` 重编码为规范 QUANTITY |
| `^0x(0\|[1-9a-f][0-9a-f]{0,15})$` | `Quantity64` | `CanonicalQuantity` + 拼写上限 {0,15} |
| `^0x(0\|[1-9a-f][0-9a-f]{0,63})$` | `Quantity256` | 同上，{0,63} |
| `^0x[0-9a-f]{64}$` | `Hash` | `ValidateWireFixedHex(64, "hash")`，小写归一 |
| `^0x[0-9a-fA-F]{40}$` | `Address` | `ValidateWireFixedHex(40, "address")`，小写归一（无 EIP-55） |
| `^0x[0-9a-f]{512}$` | `Bloom` | `ValidateWireFixedHex(512, "logsBloom")` |
| `^0x[0-9a-f]{16}$` | `BlockNonce` | `ValidateWireFixedHex(16, "nonce")` |
| `^0x[0-9a-f]{0,64}$` | `StorageKey` | 正则直验（getProof storageProof key：官方允许空/奇数长度） |
| `^0x([0-9a-fA-F]?){1,2}$`（type/v/yParity） | `HexByte` | `wire.CanonicalHexByte`：按 return-value-design.md §5 输出 canonical QUANTITY（`0x4`、`0x04`、`0x04A` → 统一 `0x4`） |
| number 字面量域（feeHistory gasUsedRatio） | `Ratio` | `wire.CanonicalRatio`：`[0,1]` 内 JSON number，最短形式 + big.Rat 往返一致；Marshal 输出无引号数字 |
| `^\\d$`-类 decimal (net_version) | `DecimalString` | `NormalizeDecimalString` |
| 单字面量 pattern（`^0x4$` 等） | `Quantity` + 常量检查 | 对象 Parse 内联 const gate |
| 未匹配的 string pattern | `Text` | 正则参数变量（deterministic `textPatternN`） |

非 string 属性：number → `Ratio`（非字符串 marshal）；boolean → `bool`；`{"const": v}` 
数值节点 → `int`/`int64`；
`{"const": str}`/字面量 pattern 节点 → 具体 string 类型 + 常量检查。

const / pattern 约束统一挂在 `fieldModel` 上（`constValues`、`patterns`），由对象
Parse 在 canonical 字段序中内联检查 —— 与手写编码器的 admit 模式完全一致。

### 3.2 对象

- **canonical 字段序**：官方文档的 properties 顺序优先；已带手写金标的对象族
  （Log、Receipt、AccessListResult/Item、FeeHistory）用 `fieldOrderOverrides`
  钉现有金标序以达成 parity。
- **nullability**：非 required 属性按出现与否渲染 `*T` + `omitempty`；required
  属性填值后不得缺省（Parse 内联 `missing required field` gate）。`null` 字面值
  永远被剔除（`PresentNonNull` gate）。
- **Raw 收集 struct**：所有字段先收成 `json.RawMessage`，再按 canonical 字段序逐个
  校验——与手写编码器相同，保证字段 admit 顺序与拒收路径稳定。

## 4. oneOf / tagged unions（Simulate CallResult 等）

官方 oneOf/anyOf 分支用 tagged union wrapper 表示：每个分支一个 pointer 字段，
Parse 按官方声明顺序 try-parse，首个匹配得胜。
- **判别顺序 = 官方声明顺序**。озвращ第一个成功分支 —— 与官方 spec 语义（branches
  正交、字段集不同）等价；对 Simulate CallResult（status/value/error）这类共享
  字段的合并分支，try-parse 里 field 约束（const、pattern、缺省 required）把
  端点约束完整转到哪一个分支判别。
- **null 提升**：oneOf 含 null 分支时，null 分支消失，empty wrapper（nil 全部
  字段）表示 JSON null（`Not Found` 语义）；root 的 null 分支转 wrapper 指针。
- **envelope style（properties + oneOf）**：官方 getTransactionByHash 的
  "Transaction information"，merge rules：base properties 并进每个 object branch，
  branch 定义覆盖冲突；required 是并集。合并后的分支对象与块内容中同形的
  schema 按指纹去重，复用同一个 Go 类型。
- **数组分支 [hashes | full transactions]**（block.transactions）：每个数组分支
  生成 `(T, bool)` parser（`parse<Union><Branch>`）：official 数组形状要么整体校验
  通过、要么该分支不匹配。
- **数组套数组**（feeHistory reward 的 `[][]Quantity`）：数组循环递归展开；更深
  的嵌套深度（或数组分支再嵌数组）在生成期 panic（不变量 #4）。

## 5. 命名

顺序：path override（钉稳定地点：SimulateCallResult / SimulateCallError /
SimulateError{Revert,VM} / AuthorizationTuple / BlockTransactions）→
title override（官方 title 映射：Receipt information→Receipt、Block object→Block、
"feeHistoryResults"→FeeHistoryResult、Account proof→AccountProofResult 等）→
`sanitizeTitle` + 冲突 uniqueName（`Result<n>` 兜底）。

**未经官方 title 的 schema（如 authorization tuple item）**：path override 钉名。
所有 override 使用报告（`visited`）标注；path 从 pinned bundle 消失即 `unused
override` 注释进入生成日志（audit trail）。

union 的 branch 字段名来自 branch title（`Signed 7702 Transaction`→
`Signed7702Transaction`）；无 title 的 fallback 采用分支自身类型名
（`SimulateErrorRevert` 等）。

## 6. 方法的 root shims

每个官方 result 生成 `New<MethodCamel>Result(raw)`：

- 非 nullable object/union root：直接复用 `New<X>`，不额外生成 shim。
- nullable object/union root（Not Found 语义）：`*T` shim；JSON null → nil。
- （nullable）array root：`[]T` shim + `emitArrayInto` 循环；null → nil。
- root override（`eth_getLogs` 钉 `oneOf[1]`）：项目 canonical 只接受 logs 数组，
  官方的 array-of-hashes 分支不进 shim。

24 个方法的 root shim 齐备（`getBlockByHash`、`getTransactionByHash` 走 nullable
union/object 形态）；请求参数的准入语义仍由项目 methods 层负责，生成层只做
result decode/encode。

## 7. parity 测试覆盖

- `rpc/internal/wire/types_parity_test.go`：对官方 `.io` vectors 中携带受保护
  金标的 4 个方法（eth_getLogs、eth_getTransactionReceipt、eth_createAccessList、
  eth_feeHistory），生成层 parse+marshal 与手写编码器字节对比；null receipt 额外
  断言 shim 的 nil 语义。
- `rpc/internal/wire/types_simulate_test.go`：SimulateV1 tagged union 的
  fixture（failure / VM error / success / branchless reject），canonical 输入
  的 parse→marshal round trip 必须逐字节还原。
- `rpc/internal/wire/version_interlock_test.go`：把版本 interlock 钉在公开表面
  （`spec.Version()`），bundle 漂移时在这里先给出可读失败，早于生成文件 init
  panic。

**覆盖边界**（记录避免误读）：block 族与 transaction 族（eth_getBlockByHash /
ByNumber、eth_getTransactionByHash）的现有项目路径仍走 geth re-encode
（`go-ethereum-compatibility-policy.md`）。这些方法的生成类型已产出并接受官方
vectors 的 round-trip 验证，但**不构成字节 parity 输入**——项目还没有这些对象族
的手写金标。等生成层成为 canonical（→§9 第 3 步）后，这里的 round-trip 覆盖
升级为全方法 parity。

## 8. SimulateV1 bundle 的例外

eth_simulateV1 没有官方 conformance vectors（execution-apis 仓库无对应 .io），
所以不走 `methods.list`，而是以 `internal/spec/simulatev1/simulate.json` 单方法
bundle 纳入：`internal/spec/builder.BuildMethod` 从官方 beta.7 openrpc.json 的
dereference 状态提取（2 个 params + result schema），版本与 `methods.json` 同钉
v1.0.0-beta.7，经 `spec.SimulateV1JSON()` 供生成器读取。有效性由 §7 的 simulate
round-trip fixtures 验证承担。

## 9. 迁移路径

本步只把**类型层**切换到标准定义；调用侧（`gethcompat/`、objects/*）尚未搬到
生成类型之上。后续分三个子步骤：

1. **objects 层之内**：金标已 parity 的 4 个编码器继续承担 canonical 编码，同时
   作为渐进迁移的字节参考。
2. **方法接入**：root shim 已覆盖的方法（block 族、simulate、receipts 等）从生成
   层承担 parse/re-encode，`gethcompat` 按方法逐个退役。
3. **全面切换**：46 个方法的响应类型全部来自生成层；go-ethereum 的 `types.*`
   数据形态只在 `gethcompat` 边界出现，不再进入 canonical wire 域。

每个子步以 §7 的 parity / round-trip 测试为检核：新接入的方法把_vectors 形态的
round-trip 收进检核集，门槛随覆盖移动，而不是随配置放宽。

## 10. 修改流程

1. 修改官方 schema、生成器模型或 override 表。
2. `cd rpc/internal/wire && go run ./gen -out types.gen.go`。
3. `go build ./... && go test ./rpc/internal/wire/`：parity / round-trip 必须全绿。
4. 若 bundle 版本变更：同步升级 `internal/spec` 的 VERSION、`methods.json`、
   `simulatev1/simulate.json`；漂移两侧不一致时 `spec.Version()` interlock 直接
   panic，编译期即可暴露。
5. `types.gen.go` 必须进出仓（生成的文件就是 canonical 类型层）；提交时一并审阅
   生成器的 stderr 告警：`unused override`（override 指到的路径从 pinned bundle
   消失）与命名冲突改名的 uniqueName 报告。两类告警都是 reviewable diff。

