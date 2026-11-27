# internal/spec — 官方标准参考数据

## 是什么

本目录保存 ethguard 使用的以太坊执行层官方标准参考数据，来源是
[`ethereum/execution-apis`](https://github.com/ethereum/execution-apis)
的固定 commit `981aa66d79364be8322a9c358aaf0a571cca0873`。`VERSION` 使用完整 commit 身份，不把主干快照标作正式 release。它提供可比较 RPC 返回对象的官方形态与约束，并为比较字段白名单
和生成结果类型提供共同来源。额外必填规则、请求绑定和内容承诺仍由对象/方法契约决定。

* `errata/*.json`：项目审核的标准勘误，独立于原始上游数据；携带固定版本／commit、目标、输入／输出摘要、依据、退出条件和 RFC 6902 patch。只有共享 `internal/executionapi.Read` 应用；`ReadOriginal` 提供不应用勘误的独立来源视图。
* `SOURCE.json`：官方仓库、固定 commit、dereferenced OpenRPC 摘要与两个 bundle 的 SHA-256。
* `requests.json`：49 个官方 eth_／net_version 的原始参数描述；不作为运行时方法注册表，当前 CallMsg、BlockReference、FilterQuery、TransactionReceiptsQuery 和 Simulate payload／base 已有生成入口及公开接入，其余库存不代表已消费。
* `VERSION`：固定的上游 commit 身份（单源），与两个 bundle、来源清单及勘误一致。
* `methods.list`：项目主动纳入标准边界的方法名清单；它只选择方法，不定义结果
  schema。结果 schema 必须由官方 dereferenced OpenRPC 生成。
* `methods.json`：26 个参考方法的**结果契约裁剪**——每个方法名映射到它在官方
  `openrpc.json` 中的 `result.schema` 原文（verbatim，无变换），外加顶层
  `version` 自描述。约 100KB。这里包含 `eth_getProof`，即使它目前仍是
  `UnsupportedMethod`。
* `vectors/`：官方 `tests/*.io` 抽取的 conformance 向量（22 个方法目录，共 204 个 `.io`，包含 `eth_simulateV1` 的 95 个原始向量）；`vectors/SOURCE.json` 记录固定来源和逐文件摘要，目录库存与清单双向核对。
  `internal/spec/conformance` 会离线解析每个 request/response 对，并按 pinned
  result schema 检查 result 的类型、required、pattern、数组约束和未知字段。
  结果 schema 尚未纳入 bundle 的官方向量会在报告的 `Uncovered` 中显式列出；当前
  官方向量已全部覆盖，包括 `eth_sendRawTransaction` 的 32 字节交易哈希结果。
* 两个官方 `eth_getHeaderByHash`／`eth_getHeaderByNumber` 的结果合同纳入同一bundle，用于生成独立Header；不由此注册或切换运行时RPC。该次扩充未修改请求库存及当时三项勘误，历史记录在 `migrations/*-header-models.json`；后续 DATA 增强见下文。
* `eth_simulateV1` 与其他方法共用 `methods.json`／`requests.json` 和 `vectors/SOURCE.json`；其向量保存在 `vectors/eth_simulateV1/`。95 个向量包含 64 个 result 和 31 个顶层错误；顶层错误不进入 result parser。旧 pin 的两个 VM 消息／schema 冲突已由上游修复，不保留旧版测试侧消息替换。详见 `docs/design/wire-codegen.md`。
* `spec.go`：唯一的例外手写代码——`go:embed` 访问器（embed 必须与文件同目录，
  别无他法）。无逻辑、无判断，只暴露字节。

## 数据流向（单向，无回路）

```text
上游 YAML (ethereum/execution-apis @ VERSION)
   │  官方构建：tools/specgen -deref  →  openrpc.json (78 方法全消解, ~2.5MB)
   │  verbatim 裁剪：取 methods.list 中的 result.schema → methods.json
   │  项目 errata/*.json（固定来源及摘要，原件不改）
   ├── executionapi.Read → 有效 schema → Mask Tree／官方字段 projection
   └── executionapi.Read → go generate ./ethapi → Jennifer → types.gen.go

原始 bundle／ReadOriginal → 独立来源 conformance 与升级审核
```

Mask 只负责白名单，不执行 required 检查。生成类型则执行官方 schema 的 required、
值约束和 nullable；比标准形态更强的对象/方法准入规则不会因此被取代。官方类型保留
显式 nullable 字段，受保护 Value 的额外投影由既有 encoder 负责，不能反过来裁剪 schema。

数值类型及其约束也以 bundle 的 `type`、`pattern`、`const`、`minimum`、`maximum`
为准。生成的 `ethapi.Number` 精确保留十进制值，不经 float64；受保护 encoder 的
`ethcodec.CanonicalRatio` 政策不能反向收窄官方数值模型。生成边界与验证方法见
`docs/design/wire-codegen.md`。

当前前置增强共有25份勘误：原有pending上下文、Simulate items和getLogs空数组三项，以及22份 `wire-data-*`。后者依据 EIP-1474 将64个DATA节点的pattern从 `^0x[0-9a-f]*$` 改为 `^0x([0-9a-f]{2})*$`，覆盖13个结果根和9个请求参数根，保留空值与前导零，不改变StorageKey／HexByte。每份文件分别给出依据、逐路径test/replace、输入／输出摘要和退出条件；按文件名排序在原结构勘误之后应用，输入摘要标识当时实际有效根。原始bundle和官方向量保持不变，`ReadOriginal`仍暴露原DATA定义的缺口。

这种“前置增强 + 官方兜底”把标准修补与通用生成分开：构造器先验证有效schema的输入约束，再执行明确的合法值表示政策。运行时provider大小写兼容和字段投影由实际消费者显式处理，不混入上述固定数据勘误。当前加载器要求勘误目标已存在，整项无schema的补充需要具体需求和独立设计，不据此声称所有官方缺失都已覆盖。

## 来源可复现（升级/审计用）

构建官方文件（需 Go 工具链，不需 npm）：

```bash
git clone https://github.com/ethereum/execution-apis.git /tmp/execapis
git -C /tmp/execapis checkout "$(cat internal/spec/VERSION)"
git -C /tmp/execapis sparse-checkout set tools src tests
go -C /tmp/execapis/tools build -o /tmp/specgen ./cmd/specgen
(cd /tmp/execapis && /tmp/specgen -o /tmp/openrpc.json -deref \
  -schemas 'src/schemas' -schemas 'src/engine/openrpc/schemas' \
  -methods 'src/eth' -methods 'src/debug' -methods 'src/txpool' \
  -methods 'src/engine/openrpc/methods' -methods 'src/testing' \
  -error-groups 'src/error-groups')
```

回到本项目根目录，用现有工具从 `methods.list` 读取方法集合，保持结果 bundle 的确定性格式；不要另写 JSON emitter 改变原始 schema 字节：

```bash
go run ./cmd/specbundle \
  -openrpc /tmp/openrpc.json \
  -version "$(cat internal/spec/VERSION)" \
  -methods internal/spec/methods.list \
  -out /tmp/methods.json \
  -requests-out /tmp/requests.json
```

核对 OpenRPC 和两个 bundle 的 SHA-256 与 `SOURCE.json` 一致；所有向量使用一个 `vectors/SOURCE.json`，逐文件摘要与实际库存双向一致，并可与同一固定 checkout 的 `tests/<method>/*.io` 原件逐字节复核。

旧 bundle 也可以通过 `-methods-from` 读取方法名，但升级时应优先使用
`methods.list`，让生成物中的 schema 变化全部来自官方 OpenRPC 输入。

升级流程：选择官方固定来源 → 重跑上面构建与裁剪 → 用
`go run ./cmd/specdiff -before old/methods.json -after new/methods.json` 看
方法和字段级 diff（含 type/required 变化）→ 为每一项变化填写迁移 manifest，标记
conformance 与 replay 已通过 → 用
`go run ./cmd/specdiff -check -before old/methods.json -after new/methods.json \
  -migration spec-migration.json` 作为 CI 门禁 → 跑对象切片、向量、R4 与全量测试。

`-check` 要求 manifest 精确列出每个新增、删除或字段变化的 method/path/kind，且
同时提供 `conformancePassed` 和 `replayPassed`。因此候选 bundle 不能只更新标准数据，
必须留下可审计的迁移记录和验证证据。

[历史结果升级记录](migrations/981aa66d79364be8322a9c358aaf0a571cca0873.json)覆盖当时 23 方法 `methods.json` 的 6 个方法、32 项变化，并绑定当时前后 bundle 摘要；它不改标为随后扩充的当前方法集合摘要。随后同一 commit 的[来源布局归一记录](migrations/981aa66d79364be8322a9c358aaf0a571cca0873-unified-layout.json)把已有 Simulate result 纳入统一结果 bundle、95 个向量移入共享目录；仅改变收纳位置，不改变任何原始 schema／请求 descriptor／向量字节／勘误。`TestUnifiedLayoutPreservesHistoricalSourceSnapshots` 从当前统一来源重建旧 JSON envelope 并核对历史摘要。结果 diff 仍不覆盖请求库存，不能从迁移记录推导完整请求语义已经迁移。

## 审计指引

1. `git diff methods.json`：上游变更的精确清单（小文件，可逐行审）。
2. `internal/ethobject/wiremask/prune.go`：规范字段 mask、剪枝和现有 hex 规范化；字段归属不能替代值有效性检查。
3. 向量 + R4 + 关键字段断言：可执行的一致性。
4. `VERSION`：与 `methods.json` 内 `version` 的互锁由加载器强制（不一致直接报错）。
5. `go test ./internal/executionapi -run '^TestErrata' -v`：核对审计摘要、来源漂移、重复／无效／冗余修正与原子失败。
6. `go generate ./ethapi`：结果类型从同版本原始 bundle 加显式勘误生成，头部记录勘误身份；CI 在临时文件中
   重新生成并与仓内产物比较。生成类型与官方向量的行为验证见
   `docs/design/wire-codegen.md`，不能以双方拒绝有效输入充当 parity 通过。

## 有意不做的事（后续优化空间）

* 不 vendor 完整 `openrpc.json`；来源摘要固定，需要时按上面命令重建。
* 不手写第二套官方结果字段定义：`ethapi/types.gen.go` 是固定 bundle 的
  生成产物，随生成器一并审阅提交，CI 检查其新鲜度。
* 不引入运行时 JSON-Schema 解释器。生成类型显式校验支持的标准形态；请求绑定、
  受保护字段投影及内容承诺继续由对象/方法层承担。
* `net_listening` / `net_peerCount` 无官方条目，暂由项目自己的方法契约覆盖；
  上游一旦收录，再评估是否迁入标准数据包。
