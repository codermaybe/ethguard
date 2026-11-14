# internal/spec — 官方标准参考数据

## 是什么

本目录保存 ethguard 使用的以太坊执行层官方标准参考数据，来源是
[`ethereum/execution-apis`](https://github.com/ethereum/execution-apis)
的固定 release（`v1.0.0-beta.7` 对应 commit `5aebdfdd45cadeb723be4bd45b4611b71c8b1c85`）。它提供可比较 RPC 返回对象的官方形态与约束，并为比较字段白名单
和生成结果类型提供共同来源。额外必填规则、请求绑定和内容承诺仍由对象/方法契约决定。

* `errata/*.json`：项目审核的标准勘误，独立于原始上游数据；携带固定版本／commit、目标、输入／输出摘要、依据、退出条件和 RFC 6902 patch。只有共享 `internal/executionapi.Read` 应用；`ReadOriginal` 提供不应用勘误的独立来源视图。
* `SOURCE.json`：官方仓库、固定 commit、dereferenced OpenRPC 摘要与三个 bundle 的 SHA-256。
* `requests.json`：43 个官方 eth_／net_version 的原始参数描述；不作为运行时方法注册表，当前仅 CallMsg 已进入请求类型生成。
* `VERSION`：pin 住的标准 release（单源）。当前 `v1.0.0-beta.7`。
* `methods.list`：项目主动纳入标准边界的方法名清单；它只选择方法，不定义结果
  schema。结果 schema 必须由官方 dereferenced OpenRPC 生成。
* `methods.json`：23 个参考方法的**结果契约裁剪**——每个方法名映射到它在官方
  `openrpc.json` 中的 `result.schema` 原文（verbatim，无变换），外加顶层
  `version` 自描述。约 100KB。这里包含 `eth_getProof`，即使它目前仍是
  `UnsupportedMethod`。
* `vectors/`：官方 `tests/*.io` 抽取的 conformance 向量（21 个方法目录）。
  `internal/spec/conformance` 会离线解析每个 request/response 对，并按 pinned
  result schema 检查 result 的类型、required、pattern、数组约束和未知字段。
  结果 schema 尚未纳入 bundle 的官方向量会在报告的 `Uncovered` 中显式列出；当前
  官方向量已全部覆盖，包括 `eth_sendRawTransaction` 的 32 字节交易哈希结果。
* `simulatev1/simulate.json`：同一官方固定来源的完整单方法请求／结果 bundle；`simulatev1/SOURCE.json` 固定 commit 和逐文件摘要，`simulatev1/vectors/` 原样保存全部 91 个官方 `.io`。通过独立 evidence FS 验证，不混入共享向量的全通过声明；62 个 result 验证声明字段完整，27 个顶层错误单独记账，另 2 个 VM 消息／schema 冲突保持列缺。详见 `docs/design/wire-codegen.md`。
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

## 来源可复现（升级/审计用）

构建官方文件（需 Go 工具链，不需 npm）：

```bash
git clone https://github.com/ethereum/execution-apis.git /tmp/execapis
git -C /tmp/execapis checkout <VERSION>
git -C /tmp/execapis sparse-checkout set tools src
go -C /tmp/execapis/tools build -o /tmp/specgen ./cmd/specgen
/tmp/specgen -o /tmp/openrpc.json -deref \
  -schemas 'src/schemas' -schemas 'src/engine/openrpc/schemas' \
  -methods 'src/eth' -methods 'src/debug' -methods 'src/txpool' \
  -methods 'src/engine/openrpc/methods' -methods 'src/testing' \
  -error-groups 'src/error-groups'
```

裁剪（标准库 python 即可，无依赖）：

```python
import json
doc = json.load(open('/tmp/openrpc.json'))
wanted = [<methods.list 中的方法名>]
out = {"version": "<VERSION>",
       "results": {m["name"]: m["result"]["schema"]
                   for m in doc["methods"] if m["name"] in wanted}}
assert len(out["results"]) == len(wanted)
json.dump(out, open("methods.json", "w"), indent=1, sort_keys=True)
```

仓库内的等价可重复入口从 `methods.list` 读取方法集合，避免把 provider 反馈带入
标准边界：

```bash
go run ./cmd/specbundle \
  -openrpc /tmp/openrpc.json \
  -version <VERSION> \
  -methods internal/spec/methods.list \
  -out /tmp/methods.json \
  -requests-out /tmp/requests.json
```

旧 bundle 也可以通过 `-methods-from` 读取方法名，但升级时应优先使用
`methods.list`，让生成物中的 schema 变化全部来自官方 OpenRPC 输入。

升级流程：上游发版 → 重跑上面两步 → 用
`go run ./cmd/specdiff -before old/methods.json -after new/methods.json` 看
方法和字段级 diff（含 type/required 变化）→ 为每一项变化填写迁移 manifest，标记
conformance 与 replay 已通过 → 用
`go run ./cmd/specdiff -check -before old/methods.json -after new/methods.json \
  -migration spec-migration.json` 作为 CI 门禁 → 跑对象切片、向量、R4 与全量测试。

`-check` 要求 manifest 精确列出每个新增、删除或字段变化的 method/path/kind，且
同时提供 `conformancePassed` 和 `replayPassed`。因此候选 bundle 不能只更新标准数据，
必须留下可审计的迁移记录和验证证据。

## 审计指引

1. `git diff methods.json`：上游变更的精确清单（小文件，可逐行审）。
2. `rpc/methods/prune.go`：通用编译器 + 剪枝引擎，手写，可单测全覆盖。
3. 向量 + R4 + 关键字段断言：可执行的一致性。
4. `VERSION`：与 `methods.json` 内 `version` 的互锁由加载器强制（不一致直接报错）。
5. `go test ./internal/executionapi -run '^TestErrata' -v`：核对审计摘要、来源漂移、重复／无效／冗余修正与原子失败。
6. `go generate ./ethapi`：结果类型从同版本原始 bundle 加显式勘误生成，头部记录勘误身份；CI 在临时文件中
   重新生成并与仓内产物比较。生成类型与官方向量的行为验证见
   `docs/design/wire-codegen.md`，不能以双方拒绝有效输入充当 parity 通过。

## 有意不做的事（后续优化空间）

* 不 vendor 完整 `openrpc.json`（2.5MB，无人可审；需要时按上面命令重建）。
* 不手写第二套官方结果字段定义：`ethapi/types.gen.go` 是固定 bundle 的
  生成产物，随生成器一并审阅提交，CI 检查其新鲜度。
* 不引入运行时 JSON-Schema 解释器。生成类型显式校验支持的标准形态；请求绑定、
  受保护字段投影及内容承诺继续由对象/方法层承担。
* `net_listening` / `net_peerCount` 无官方条目，暂由项目自己的方法契约覆盖；
  上游一旦收录，再评估是否迁入标准数据包。
