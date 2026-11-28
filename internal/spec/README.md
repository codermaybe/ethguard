# 固定官方来源

本目录保存 execution-apis 固定 commit 的原始提取物；不在这里手改 schema 或官方 `.io` 向量。项目生成选择和命名由 [`internal/executionapi/manifest.json`](../executionapi/manifest.json) 统一声明，标准修正在 [`errata/`](errata/) 明确记录。完整维护合同见 [wire-codegen.md](../../docs/design/wire-codegen.md)，SDK 用户入口见 [ethapi/README.md](../../ethapi/README.md)。

当前 pin 为 `981aa66d79364be8322a9c358aaf0a571cca0873`。该来源包含 `net_listening` 和 `net_peerCount` 的官方定义；两者不需要项目补充 schema。

| 材料 | 内容和身份 |
| --- | --- |
| `VERSION` | 完整上游 commit |
| `SOURCE.json` | repository、commit、来源类别、官方 specgen 构建参数与工具链、OpenRPC／三个提取物／向量清单摘要 |
| `methods.json` | Manifest 选择的 31 个原始 result schema |
| `requests.json` | Manifest 的 `eth_`／`net_` 请求库存，当前 51 个方法；保留参数名称、顺序、required 和原始 schema |
| `inventory.json` | 完整 OpenRPC 的 93 个方法；记录 `/methods/N` 原始位置、结果与参数 schema 摘要、完整有序参数描述符摘要 |
| `vectors/` | 31 个方法的 226 个官方 `.io` 文件；其中 227 条 RPC 交换含 185 个成功 result 和 42 个顶层错误 |
| `vectors/SOURCE.json` | 官方向量来源和逐文件原始字节摘要 |
| `errata/` | 三份结构 patch 与一份有限 DATA 规则，独立于官方原件 |
| `migrations/` | 不改写的历史迁移记录、退役的历史勘误定义及旧向量清单；不参与当前 Overlay 加载 |

这些数字描述当前 pin 和选择，不能替代库存校验。Header 等已生成但未注册的模型、已收录但尚未消费的方法、没有官方向量的方法分别出现在生成覆盖总账；不能要求所有集合相等。

`executionapi.ReadOriginal()` 验证来源清单、提取物和库存后返回未修正定义。`Read()` 再原子应用全部审定 Overlay。显式 `ReadOriginalFrom(fs.FS)`／`ReadFrom(fs.FS)` 接受以本目录为根的候选文件系统；它们不加载 Manifest 或生成审计。逐向量文件核验由 `specsync` 和独立来源测试承担，运行时装配只核对向量清单身份。

升级统一使用 `go run ./cmd/specsync`。联网固定 commit、离线干净 checkout、离线已审定材料三种输入互斥；命令把原件、诊断、生成文件和验证证据写到全新的仓外候选目录。命令参数、离线来源信任与审阅流程集中在 [工程合同](../../docs/design/wire-codegen.md#可复现同步与候选验收)，本目录不再维护一套手工同步步骤。

来源与原件复核入口：

```sh
go test ./internal/spec/builder ./internal/spec/conformance ./internal/executionapi
go run ./cmd/specdiff -before-dir internal/spec -after-dir /tmp/candidate/spec -json
```

普通使用者直接使用已提交的 `ethapi` 模型，无需同步来源或运行生成器。
