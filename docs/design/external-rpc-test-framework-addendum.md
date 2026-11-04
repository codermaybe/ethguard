# 外部 RPC 测试框架 · 增补说明

状态：**增补说明（2026-11-04）**。本文件是对
[external-rpc-test-framework.md](external-rpc-test-framework.md) 的补充与局部修正，
由一次设计评审产生（评审结论：分层方向成立，不需更换框架，但需修正四类缺口）。

本文件**不重写主文档正文**，不改变生产代码、Decision、quorum、reference、typed
projection 或方法数量，不改变工具层与核心运行时的依赖方向。主文档 §2.2、§4.1、
§4.2、§5、§6、§8、§10 按本文件对应条目执行，未列入的条目仍按主文档执行。

关联：[external-rpc-test-framework.md](external-rpc-test-framework.md)、
[live-canary-contract.md](live-canary-contract.md)、[tooling.md](tooling.md)、
[capture-golden-contract.md](capture-golden-contract.md)、
[project-roadmap.md](project-roadmap.md)。

适用阶段：切片 15 与最终审计之后的「外部 RPC 测试框架实现」轨道（见 roadmap
同名小节）。实现前先读本文件 §0 与 §9。

---

## 0.1 轨道衔接状态（2026-11-04 确认）

前置条件已满足：切片 1–15 完成并通过最终审计（审计报告
[final-audit-report.md](final-audit-report.md)，门禁证据见该文 §门禁证据），
单线执行模型、context 收敛、reference stage 共用与 typed 归位均已提交。
本轨道因此可以按 §9 顺序 1 启动，并受以下两条衔接约束：

1. **增补是纯增量，不阻断现有部分。** 实现只新增测试文件、fixture 与 CI job；
   既有测试与门禁必须保持绿（全量 test、race、架构/default-build interlock、
   Verification 专项）。任何需要改动既有测试断言或生产语义才能成立的场景，
   记为该场景 `inconclusive` 并上报，不属于本轨道的通过方式。
2. **互锁先行核对。** `TestDefaultBuildExcludesEthclient` 已禁止默认构建与
   WSS 生产路径链接 gorilla/websocket；§4 的交叉校验测试只允许出现在
   `_test.go` 中（`go list -deps` 不含测试依赖，互锁保持有效），落地该条时
   必须先跑架构互锁确认无回归，再提交。

---

## 0. 给实现者的使用规则

1. 本文件是**增补**。与主文档冲突时，以本文件为准，但只限本文件明确列出的条目。
2. 本文件**不修改主文档**。主文档的回写（交叉引用与命令清单）在主文档 §11 规定的
   时机单独进行，避免与在途改动冲突。
3. 实现顺序按 §9 表格单线推进；退出条件未满足时停止，不进入下一行。
4. §7 的「暂不引入」是硬约束：不得为了让某个场景通过而临时引入。

---

## 1. 外部来源的唯一白名单（新增，约束所有层）

这一条优先于其余所有条目。

- 任何一层的**外部参考来源**都只能是 Chainlist 公共注册表
  （`https://chainlist.org/rpcs.json`）中的条目，且必须能通过
  `chainlist-sorted-v2` 的凭据过滤（[live-canary-contract.md](live-canary-contract.md)
  §3.2 步骤 4–6：非 https、userinfo、`apikey`/`api_key`/`key`/`token` 查询参数、
  path 内嵌凭据全部记为 `credentials` 并剔除）。
- 禁止把下列来源用作证据来源或 fork 来源：私有端点、自建或自托管的 RPC、
  带凭证或带 key 的 URL、任何为了让测试通过而临时加入的个人端点。
- 主文档 §4.1 的「从固定 fork URL 启动 Anvil」据此收窄为：**fork URL 必须来自
  Chainlist 公共注册表且已去凭据**，并在 fixture manifest 中记录该端点的
  `source_id` 与 endpoint identity，使其可公开复核。
- 公共端点无法提供所需历史状态（archive 缺失、限流、超时）时，该场景记为
  `inconclusive` / not executable，**不得**改用私有端点补齐。这是本条的强制后果，
  不是可选项。
- 证据中出现任何非 Chainlist 来源的端点时，该份证据**无效**（不是降级采信，是
  不予采信），并记录为需要复核的偏差。

理由：live canary 现有的可信度来自「公共注册表 + 去凭据 + 可公开重建的 voting
basis」。测试框架若允许个人端点进入证据链，等于把「某人的环境能通过」写成
「公开参考通过」，这会直接摧毁该层证据的全部价值。

---

## 2. Anvil 层改为「离线 fixture」（修正主文档 §4.1、§4.2）

主文档允许 fork URL 在测试时联网，并按缺失情况 `t.Skip`。这两条合起来会让该层
可以长期不产出任何证据。改为抓取与执行分离：

| 阶段 | 时机 | 网络 | 产物 |
| --- | --- | --- | --- |
| 抓取 | 定时 workflow（允许联网，来源按 §1 限定） | 是 | 状态快照 + manifest（含 digest、fork block、block hash、anvil 版本） |
| 执行 | 默认 CI 与本地测试 | **否** | 测试结果 + 引用上面的 fixture |

- 执行阶段使用 `anvil --load-state` 加载已落盘的状态，**不联网、不带凭证**。
- **版本必须钉死**：启动器指定明确的 anvil 版本（如 `foundryup --version <x>`），
  禁止使用 runner 上碰巧存在的任意版本；版本写入 manifest。
- **provenance 复用 livecanary 的格式与校验**（manifest / schema / `verify` 命令的
  思路），不新建第二套证据格式；能复用同一套 digest 校验就复用。
- **「skip 不算证据」**：`t.Skip` 只允许出现在快速路径；被跳过的运行必须落一条
  `inconclusive` 记录（谁跳过、原因、缺少什么），不允许静默通过。
- 缺 anvil、缺 fixture、fork 状态不可得，一律记为 `inconclusive`，不是 `passed`。
- 至少一个 job **必须**跑该层并产出实际结果；否则该层不成立。

---

## 3. 时间敏感场景使用 `testing/synctest`（补充主文档 §5、§6）

主文档的 `http-delayed-response`、context deadline/取消、WSS 写阻塞、关闭后清理、
重复 Close 等场景，若用真实 `time.Sleep` 做断言，就是 flake 来源。

- 工具：`testing/synctest`，**Go 1.25 标准库**（本地工具链 go1.25.3 已包含该包），
  不引入任何新依赖。
- 规则：禁止用真实睡眠做正确性断言；防御性上界可以保留，但判定必须由假时钟驱动。
- 验收：这类场景要能连续大量重复运行而不失败（例如 `-count=200` 或等价循环），
  且不依赖机器速度。

---

## 4. WSS 增加独立实现对端的交叉校验（补充主文档 §6）

问题：生产 WSS client 与 `internal/testkit/wstest` 都是本仓库自研，同源。握手或帧
语义上同一个理解错误会被两边同时接受，测试通过而双方皆错 —— 这是该层证据质量上
最实际的漏洞。

- 要求：**至少一条**测试让生产 WSS client 与一个独立、成熟、被广泛使用的实现完成
  握手与一次完整请求/响应。
- 选择：`github.com/gorilla/websocket`。仓库模块图中已有 `v1.5.3`（当前为 indirect，
  `go.sum` 已有，且不在默认构建依赖内），作为**测试专用**直接依赖不产生新的模块
  下载；`go.mod` 会增加一条 direct 条目（版本不变），提交说明中写明。
- 约束：只用于交叉校验，不作为业务测试主路径；不改变 [tooling.md](tooling.md) 的
  依赖方向；生产包不得导入它（`TestDefaultBuildExcludesEthclient` 对默认构建与
  WSS 生产路径的 gorilla/websocket 禁令保持有效——该依赖只允许进入 `_test.go`，
  `go list -deps` 不含测试依赖，互锁不受影响）。
- 主文档 §2.2 的「不建第二套 WSS 测试设施」仍然有效：本条的定位是**独立 oracle**，
  不是第二套 `wstest`。若将来更换其它实现，只改这一条测试，不扩散。

---

## 5. `not_observed` 的落地方式（修正主文档 §8）

主文档 §8 要求报告状态区分四态，其中 `not_observed` 目前并不存在：

- [live-canary-contract.md](live-canary-contract.md) 已冻结三态；
- `docs/design/live-canary/report.schema.json` 的 `state` 是
  `enum: [passed, failed, inconclusive]`，且顶层 `additionalProperties: false`。

因此第四态**不能**直接加入顶层 `state`：

- 采用：把 `not_observed` 表达为 `inconclusive` 下的一个 `state_reason` 类别
  （或在 plan / manifest 层记录「本轮未观察到的目标形态」，例如真实
  `error.data: null`）。schema 不变，contract 不变。
- 若确实需要独立顶层状态，则单独立决策 + schema 版本升级 + contract 修订，
  不在本增补范围内。

---

## 6. 原生 fuzz 的定位（澄清主文档 §2.2）

事实修正：仓库**已经**有 Go 原生 fuzz target —— `internal/jsonsafe`、
`internal/rpcwire`、`internal/transport/wss`（帧）、`rpc/internal/objects/*`、
`rpc/internal/wire`。主文档 §2.2 排除的是第三方属性测试框架，不是标准库 fuzz。

- 新增要求：CI 目前只跑种子语料。增加一个**短预算的定时 fuzz job**
  （每个 target 数十秒 `-fuzztime`），失败即入库语料成为回归。
- 不引入任何新依赖；不把 fuzz 放进 PR 关键路径。

---

## 7. 明确暂不引入（登记）

| 候选 | 结论 | 原因 |
| --- | --- | --- |
| go-ethereum `ethclient/simulated` 作为链语义或差分对照 | 暂不引入 | 与 Anvil 链语义层重复；go-ethereum 保持为类型与兼容行为的版本化对照（主文档 §2.2 已定） |
| Hive / `rpc-compat` | 暂不引入 | 需要 Docker 与 client adapter，过重 |
| testcontainers / 容器化节点 | 暂不引入 | 引入 Docker 与镜像拉取网络依赖 |
| go-vcr、Toxiproxy、Hardhat、testify、第三方属性测试框架 | 暂不引入 | 沿用主文档 §2.2：重复现有标准库 seam 或把第三方运行时带进普通 CI |

---

## 8. 证据矩阵（新增，主文档 §10 的配套）

每条主张必须能指出：哪一层、哪条命令、什么产物、外部来源的合规要求、以及该层
「未运行」时的语义。

| # | 主张 | 层 | 命令 | 产物 | 外部来源 | 未运行时 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | JSON-RPC/HTTP 编码与分类正确 | contract | `go test ./internal/... -count=1` | 测试输出 | 无 | 不允许 skip |
| 2 | 故障归属正确（transport / source / business） | fault scenario（httptest + RoundTripper） | 同上 | 测试输出 + 场景名 | 无 | 不允许 skip |
| 3 | WSS 帧与连接生命周期正确 | contract（wstest + synctest） | `go test ./internal/transport/wss ./internal/testkit/wstest -race -count=10` | 测试输出 | 无 | 不允许 skip |
| 4 | WSS 与独立实现互操作 | cross-check（§4） | 该条测试 | 测试输出 | 无 | 不允许 skip |
| 5 | 真实链语义（固定块 / moving tag / EIP-1898 / 状态变化） | Anvil 离线 fixture（§2） | `go test ./internal/verification/integration -run '^TestAnvil' -count=1` | fixture + manifest + digest | 必须来自 Chainlist 公共端点（§1） | skip = `inconclusive`（记名） |
| 6 | provider 当前行为与 provenance | livecanary | `go run ./cmd/livecanary ...` | manifest / registry / report + SHA256SUMS | Chainlist 快照 | `inconclusive` 合法，但不等于通过 |
| 7 | 解码健壮性（畸形 JSON / 帧） | 原生 fuzz（§6） | 定时 fuzz job | 失败语料 + 报告 | 无 | 不允许 skip |

两条通用规则：

- **skip 不计入证据**；任何一层的「未运行」必须在报告里可见。
- 测试通过**不能**自动关闭 W4、provider compatibility 或真实 null 证据缺口
  （与主文档 §10.7 一致）。

---

## 9. 实现顺序（下游开发按此执行）

| 顺序 | 任务 | 依赖 | 退出条件 |
| --- | --- | --- | --- |
| 1 | HTTP 传输故障场景 + 证据断言 | 无新依赖 | 离线全绿；每个异常场景同时断言 Decision 与故障归属 |
| 2 | WSS 生命周期场景、synctest 化、独立实现交叉校验 | 顺序 1 的断言风格 | `-race -count=10` 稳定；交叉校验测试通过 |
| 3 | 原生 fuzz 定时化 | 无 | 定时 job 有实际结果；失败语料入库 |
| 4 | Anvil 离线 fixture（抓取/执行分离、版本钉死、公共来源校验、provenance） | 顺序 1–3 的产物风格 | 该层有实际运行结果；skip 记 `inconclusive`；证据中无私有端点 |
| 5 | 证据矩阵与命令清单落地，并按主文档 §11 回写交叉引用 | 顺序 1–4 | §8 矩阵每一行都有实际运行结果 |

顺序 1–3 全部离线、零新依赖，应先行；顺序 4 只有出现「本地 fixture 无法回答的
具名问题」时才启动。

### 9.1 当前实现状态（2026-11-04）

截至本次审计，顺序 1–3 只有部分验收项落地；不得标记这些顺序或整个框架为完成：

- HTTP 故障测试位于 `aggregator/external_rpc_http_scenarios_test.go`，覆盖 malformed JSON、真实 HTTP body 中途 EOF、429/限流能力、error-data missing/null/empty/value、vendor 扩展字段和取消中的请求；异常 Decision 与 source fault 均有断言。
- HTTP 延迟/deadline 场景位于 `internal/transport/http/delayed_response_synctest_test.go`，使用 Go 1.25 标准库 `testing/synctest` 的假时钟固定 deadline 分类，并断言底层 RoundTripper 已观察到请求取消、没有产生业务结果。该测试已连续运行 `-count=200`，不依赖真实睡眠。
- WSS 独立实现交叉校验位于 `internal/transport/wss/gorilla_interop_test.go`，检查握手、JSON-RPC id、方法、两个参数值及响应解码。`gorilla/websocket v1.5.3` 已提升为 go.mod 直接测试依赖，生产 WSS 导入/依赖门不变。
- 定时 fuzz job 只在 schedule 运行，现覆盖仓库全部 11 个原生 fuzz 函数（JSON safety、response decoder、WSS 两个 frame targets、六个 RPC object encoder、wire normalizer），每个预算 30 秒。定时 job 尚无实际运行结果；本地 smoke 不能替代该退出条件。
- WSS 已有生命周期与帧 contract 测试，但本轮尚未把相关时间敏感场景改为 `testing/synctest`；既有测试仍含真实睡眠/定时等待，因此顺序 2 仍未满足增补件 §3 和 §9 的全部要求，外部 RPC 测试框架整体不得标记为完成。

本轮实际验证与审计补充：

- `go test ./aggregator -run '^TestExternalRPCHTTP' -race -count=20`：通过。
- `go test ./internal/transport/wss -run 'TestGorillaInteroperability|TestWireLayerLinkGraph|TestNoThirdPartyWebSocketLibraryEntersTheWireLayer' -race -count=10`：通过；另见完整包 race 复核结果后补记。
- 原报告的四个 fuzz target 各 2 秒 smoke 通过；本次补跑其余七个 target 各 2 秒，结果见本记录最终验证条目。schedule 30 秒 job 尚未实际运行。
- `go test ./... -count=1 -timeout=300s`、`go vet ./...`、架构/default-build interlock：本次均通过。
- `go mod tidy -diff`：修正 gorilla 依赖分组后通过。
- `go test ./internal/transport/http -run 'TestDelayedResponseDeadlineUsesFakeTimeAndCancelsRequest|TestNativeContextPrecedence|TestNativeHTTPStatusContextPrecedence' -race -count=200 -timeout=180s`：通过。

顺序 4（Anvil 离线 fixture）仍按停止规则保持未启动：当前本地 fixture 尚无必须由 Anvil 回答的具名问题，不能将未运行记为通过。主文档 §10 所需 Anvil 场景和完整证据矩阵仍未完成；本轮仅交付离线顺序的增量部分。

---

## 10. 停止规则（沿用主文档 §10 并追加）

主文档 §10 的停止规则与验收门全部保留，追加两条：

1. 若某个场景只有使用私有、自建或带凭证端点才能通过 → 停止并上报，不得降级为
   「本地可用」，不得把该结果写入证据。
2. 若某个缺口必须靠引入第二套 WSS/RPC server、Docker 或默认 CI 公网依赖才能表达
   → 单独立项评估（见 §7），不在本轨道内临时引入。

---

## 11. 与主文档的关系与回写项

| 主文档条目 | 本文件的处置 |
| --- | --- |
| §2.2 工具选择 | §6 澄清 fuzz 定位；§7 登记暂不引入 |
| §4.1 Anvil 定位与启动器 | §1 收窄 fork 来源；§2 改为离线 fixture 与版本钉死 |
| §4.2 生成物与可复现性 | §2 规定 provenance 复用与 skip 语义 |
| §5 HTTP 故障场景 | §3 规定时间敏感场景的 synctest 化 |
| §6 WSS 与长连接场景 | §3、§4 增加 synctest 与独立实现交叉校验 |
| §8 外部证据与本地回放 | §5 修正 `not_observed` 的落地方式 |
| §10 验收门与停止规则 | §8 增加证据矩阵；§10 追加两条停止规则 |

回写时机：本增补落库后，待当前在途开发收口，再按上表修订主文档正文与
[project-roadmap.md](project-roadmap.md) 的交叉引用。回写只改引用与清单，
不改变已经生效的语义。
