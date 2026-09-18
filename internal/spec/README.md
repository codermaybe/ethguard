# internal/spec — 官方标准参考数据

## 是什么

本目录保存 ethguard 使用的以太坊执行层官方标准参考数据，来源是
[`ethereum/execution-apis`](https://github.com/ethereum/execution-apis)
的固定 release。它为可比较的 RPC 返回对象提供字段白名单；方法是否注册、哪些字段
必须存在以及如何绑定请求，仍由 `rpc/methods` 中的人工方法契约决定。

* `VERSION`：pin 住的标准 release（单源）。当前 `v1.0.0-beta.7`。
* `methods.json`：22 个参考方法的**结果契约裁剪**——每个方法名映射到它在官方
  `openrpc.json` 中的 `result.schema` 原文（verbatim，无变换），外加顶层
  `version` 自描述。约 100KB。这里包含 `eth_getProof`，即使它目前仍是
  `UnsupportedMethod`。
* `vectors/`：官方 `tests/*.io` 抽取的 conformance 向量（21 个方法目录）。
  它们是标准参考输入，尚未全部接入自动回归 harness。
* `spec.go`：唯一的例外手写代码——`go:embed` 访问器（embed 必须与文件同目录，
  别无他法）。无逻辑、无判断，只暴露字节。

## 数据流向（单向，无回路）

```text
上游 YAML (ethereum/execution-apis @ VERSION)
   │  官方构建：tools/specgen -deref  →  openrpc.json (78 方法全消解, ~2.5MB)
   │  verbatim 裁剪：取 22 个参考方法的 result.schema  →  methods.json (~100KB)
   ▼
go:embed 进二进制 → New() 时通用编译器建成内存 Mask Tree → 每请求 Prune 动态匹配
```

比较键只保留白名单字段；未知字段随胜出方的原始值走（并保留在 uncertain
抓包里可观测）。**required 语义永远不在这里**：分叉/变体导致的字段缺失只能由
各对象切片的手写门决定，mask 只做白名单。

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
wanted = [<22 个方法名，见 methods.json 的键>]
out = {"version": "<VERSION>",
       "results": {m["name"]: m["result"]["schema"]
                   for m in doc["methods"] if m["name"] in wanted}}
assert len(out["results"]) == len(wanted)
json.dump(out, open("methods.json", "w"), indent=1, sort_keys=True)
```

升级流程：上游发版 → 重跑上面两步 → 看 `methods.json` 的 `git diff`
（精确的上游变更面：某方法多了/少了哪个字段）→ 逐字段定政策
（进投票还是忽略，这是人才能做的决定）→ 按需改对象切片的手写门 →
跑向量 + R4 + 全量测试。

## 审计指引

1. `git diff methods.json`：上游变更的精确清单（小文件，可逐行审）。
2. `rpc/methods/prune.go`：通用编译器 + 剪枝引擎，手写，可单测全覆盖。
3. 向量 + R4 + 关键字段断言：可执行的一致性。
4. `VERSION`：与 `methods.json` 内 `version` 的互锁由加载器强制（不一致直接报错）。

## 有意不做的事（后续优化空间）

* 不 vendor 完整 `openrpc.json`（2.5MB，无人可审；需要时按上面命令重建）。
* 不生成 Go 文件进构建（零生成物，零新鲜度缺口；一致性靠结构而非流程保证）。
* 不在运行时做 JSON-Schema 全量校验（热路径只跑白名单剪枝；`santhosh-tekuri`
  系库仅为官方构建工具的依赖，不进本模块）。
* `vectors/` 尚未被测试消费——conformance 回归 harness 是明确的下一步。
* `net_listening` / `net_peerCount` 无官方条目，暂由项目自己的方法契约覆盖；
  上游一旦收录，再评估是否迁入标准数据包。
