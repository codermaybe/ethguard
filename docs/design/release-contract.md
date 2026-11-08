# Release Contract（发布门禁与发布契约）

状态：**现行（2026-11-09）**。本文件冻结发布入口、前置条件、门禁覆盖范围、产物
与发布语义、失败处置，并登记本轮实际证据。任务顺序、当前状态和待裁决项见
[project-roadmap.md](project-roadmap.md) 的「发布门禁与发布流程（R0–R8）」一节；
本文件不维护第二份计划或状态台账。

关联：`decisions.md` 0057（外部消费者前提）、[tooling.md](tooling.md)（工具层所有权）、
[live-canary-contract.md](live-canary-contract.md)（canary 证据分层）、
`.github/workflows/{ci,release,live-canary}.yml`、`scripts/release_check.sh`、
`scripts/govulncheck.sh`。

---

## 1. 发布入口与前置条件

- **唯一入口**：`./scripts/release_check.sh`。发布者、`release.yml` 和 `ci.yml` 的
  govulncheck job 执行同一份仓库内脚本；不得再用第二套命令表达同一语义。
- **顺序**：脚本通过 → 提交 → 推送 → 打 tag。**tag 必须指向已提交的树**：
  `release.yml` 在 tag 的检出上执行 `./scripts/release_check.sh`，未提交的脚本或
  工作流会让这次 tag 运行在 preflight 阶段直接失败。
- **tag 形态**：`v*`；预发布使用含 `-` 的名字（如 `v0.0.1-beta.N`），稳定版不含。
- **不做的事**：不移动或删除已推送的 tag；不在发布后替换已 attach 的产物。

## 2. 门禁步骤与执行位置

| 步骤 | `release_check.sh`（本地） | `release.yml`（tag） | `ci.yml`（push/PR） |
| --- | --- | --- | --- |
| `gofmt -l .` 检查 | ✔ | ✔（经脚本） | ✔（独立 job） |
| `go mod tidy -diff` | ✔ | ✔（经脚本） | ✔ |
| `go vet ./...` | ✔ | ✔（经脚本） | ✔ |
| `go build ./...` | ✔ | ✔（经脚本） | ✔ |
| `go test -race ./...` | ✔ | ✔（经脚本） | ✔，另加 shuffled/`-count=2` 与 Verification 专项门 |
| govulncheck（可达漏洞） | ✔（`scripts/govulncheck.sh`） | ✔（经脚本） | ✔（同脚本） |
| apidiff（上一 tag → 当前整模块公共 API 报告） | ✘ | ✔ | ✘ |
| fuzz sprint（jsonsafe、wire 各 120s） | ✘ | ✔ | ✘ |
| cross-compile + `checksums.sha256` | ✘ | ✔ | ✘ |
| 发布 GitHub Release | ✘ | ✔ | ✘ |

**"本地门禁通过"不等于"tag 必过"**：apidiff 与 fuzz 目前只在 tag 阶段执行
（见第 5 节）。任何声称"发布门禁已通过"的记录必须同时说明执行范围与工具链。

API 对比使用 `apidiff -m` 导出并比较整个模块的公共 API，因此也能报告被删除的包，
不依赖 `ethadapter` 等历史包路径。固定版本工具的 `-incompatible` 只筛选报告内容，
发现不兼容 API 本身不会使其退出非零；导出、加载或工具执行错误仍使工作流失败。
报告不是向后兼容通过证明，beta.3 包含相对 beta.2 的 API 删除与签名变化。

`gofmt`/`tidy`/`vet`/`build`/`test` 在脚本与 `ci.yml` 各出现一次属于允许的重复：
`ci.yml` 的对应 job 需要独立的矩阵、shuffle、race 倍数和 artifact，不能合并成
一次脚本调用；语义来源以 `scripts/release_check.sh` 为准，两边同时修改时必须
一次提交。

## 3. 工具链与固定版本政策

- **工具链来源是 `go.mod` 的 go 指令。** 本地 `go` 由 `GOTOOLCHAIN=auto` 按 go
  指令切换到下载的工具链（本机安装的 Go 可能是更旧的版本），而
  `actions/setup-go` 会显式写入 `GOTOOLCHAIN=local`（其 `src/main.ts` 的
  `setGoToolchain`），因此 CI 中生效的就是 go 指令声明的确切版本。两者不是同一
  条件：排查版本问题时必须同时记录 `go version`、`GOTOOLCHAIN=local go version`
  和 go 指令本身。
- **禁止以 `@latest` 安装工具。** 工具模块的 go 指令会静默抬高工具链下限，在
  `GOTOOLCHAIN=local` 下立即失败。当前固定版本（唯一清单，改这里就要改所有引用点）：

  | 工具 | 版本 | 引用点 |
  | --- | --- | --- |
  | govulncheck | `v1.9.0` | `scripts/govulncheck.sh` |
  | apidiff | `v0.0.0-20261014143641-f0251eb4aa3e` | `release.yml`（R1 后应移入脚本） |
  | deadcode | `v0.41.0` | `ci.yml` 定时 job |
  | golangci-lint | `v2.15.0` | `ci.yml` |

- **go 指令的取值依据是清除扫描到的标准库可达漏洞**，不是"跟到最新"；取值与证据
  见第 6.2 节。

## 4. 产物与发布语义

- **产物**：`cmd/{soak, rpcsample, shapeprobe, specbundle, specdiff}` ×
  {linux-amd64, linux-arm64, darwin-arm64} 共 15 个二进制，加 `checksums.sha256`，
  一起 attach 到该 tag 的 GitHub Release。模块本身按 `go get` 消费，不需要发布步骤。
- **预发布语义**：`release.yml` 将含 `-` 的 tag 显式标记为 prerelease，避免 beta
  被当作正式 Latest。历史 `v0.0.1-beta.2` 的 Release 仍为 `prerelease=false`
  （2026-11-09 API 实测），本次不修改旧 Release；稳定版另需明确 `make_latest`。
- **与 canary 的关系**：同一 tag 触发 `release.yml` 与 `live-canary.yml` 两个独立
  工作流，release 不等待 canary 结论。稳定 tag 的 canary 失败不会阻止 Release
  发布；预发布 tag 仅放行 `inconclusive`，`failed` 仍失败（`live-canary-contract.md`）。
  是否让 release 等待 canary 结论是待裁决项（roadmap R8）。
- **消费方工具链下限**：go 指令就是模块最低 Go 版本。`go 1.26.10` 意味着更旧的
  工具链在 `GOTOOLCHAIN=local` 下无法构建本模块（`auto` 会去下载工具链）。
- **模块代理状态**：tag 一旦推送，模块即可被 `go get` 取用，与是否已创建 Release
  无关；因此 tag 的推送本身已经是"发布"动作。

## 5. 已知覆盖缺口（2026-11-04）

1. **apidiff 只在 tag 阶段执行**，本地门禁不覆盖——`v0.0.1-beta.1` 正是死在这一步。
2. **fuzz sprint 只在 tag 阶段执行**，本地没有对应入口。
3. **门禁不锚定提交**：脚本不检查工作区是否干净，也不打印被检查的 commit，
   无法把"通过"绑定到具体提交内容。
4. **联网属性未写明**：`release_check.sh` 头部自称 "offline gates"，但 govulncheck
   需要访问 `vuln.go.dev`；该步骤不是离线步骤。
5. **死代码**：`cross-compile` 的 `windows` 分支在平台列表中没有对应项，是不可达
   死代码。

## 6. 证据（2026-11-04）

### 6.1 `v0.0.1-beta.1` 发布失败（tag c339b5f）

| 工作流 | run | 结果 | 失败步骤 |
| --- | --- | --- | --- |
| release | 41825635461 | failure | `apidiff vs previous tag`（exit 1；开始与结束同一秒，其后 4 步全部 skipped） |
| live-canary | 41825635367 | failure | `enforce observation result`（tag 运行 + `inconclusive`，当时走严格门禁） |

release 的失败根因（本地复现同一错误文本）：该步**先**执行
`go install golang.org/x/exp/cmd/apidiff@latest`，**再**判断是否第一个 tag，因此
首次 tag 必然执行这次安装；而 `golang.org/x/exp@latest` =
`v0.0.0-20261014143641-f0251eb4aa3e` 的 go 指令是 `go 1.26.0`，当时 `go.mod` 是
`go 1.25.3`，`actions/setup-go` 强制 `GOTOOLCHAIN=local`，安装立即失败（无需下载
工具链，与 <1s 的失败一致）：

```text
go: golang.org/x/exp/cmd/apidiff@latest: golang.org/x/exp@v0.0.0-20261014143641-f0251eb4aa3e requires go >= 1.26.0 (running go 1.25.3; GOTOOLCHAIN=local)
```

同一根因类还解释了 2026-11-02 定时 CI 的两处失败：`golang/govulncheck-action@v1`
内部安装 `govulncheck@latest`（x/vuln v1.9.0，go 1.26.0）与 deadcode
（x/tools v0.52.0，go 1.26.0）。三者是**同一类缺陷**（未固定版本的 `@latest`
安装 × 工具链下限），不是三个独立问题。release 工作流里没有 govulncheck 步骤，
所以 beta.1 的"发版失败"不是 govulncheck 造成的。

### 6.2 标准库可达漏洞与 go 指令

| go 指令 | govulncheck 结果 | 证据 |
| --- | --- | --- |
| `go 1.25.3`（临时副本） | **30 个可达标准库漏洞**（GO-2025-4155、GO-2026-4337…6809 等）；`GO-2026-6809`（crypto/x509 中间证书信任）的修复版本是 `go1.26.10`；trace 经 `internal/transport/wss/websocket_handshake.go:229` | 本机临时副本，仅改 go 指令，扫描器 v1.9.0 |
| `go 1.26.10`（当前） | 可达 **0**；导入包中 1 个不可达漏洞，无 require 模块可达漏洞 | `./scripts/release_check.sh` 实跑通过 |

即：提升 go 指令不是形式要求，而是清除真实可达标准库漏洞的手段；`1.26.10` 的
取值由 `GO-2026-6809` 的修复版本决定。

### 6.3 成功路径与 tag 处置

`v0.0.1-beta.2`（b6e7829）的 release（run 41829485059）与 live-canary
（run 41829485063）均成功，Release 含 16 个资产。`b6e7829` 相对 `c339b5f` 只改了
3 个工作流与 1 个设计文档，**不含任何 `.go` 改动**，因此 beta.1 与 beta.2 的模块
API 完全相同；beta.1 tag 保留为"有 tag、无 Release"状态，不删除、不补发。

## 7. 失败处置

1. tag 运行失败时**不改写、不删除、不移动**该 tag（模块代理可能已缓存），也**不重跑**
   同一 run（工作流取自该提交，结果相同）。
2. 修复后打新的 tag；失败 tag 的最终处置（补 Release / 作废）由主维护者决定，
   默认保留现状，并在下一个 Release notes 中说明。
3. 发布产物 attach 之后视为已发布证据，不得替换 `dist` 内容。
4. 任何"已通过"的记录必须写清执行范围（第 2 节）、工具链（第 3 节）和未观察项。

## 8. 外部兼容缺口的显式交付模式

当前锁定的 go-ethereum `v1.17.10` 在默认根包架构下无法把官方
`SimulateV1` 接入 `EthClient`；该事实由兼容性政策记录，不能被中间方法集合掩盖。
默认 `release_check.sh` 仍以 `ETHGUARD_REQUIRE_COMPLETE=1` 执行并拒绝该缺口。

在本轮已接受该外部边界、但仍需完成代码与安全门禁时，可以显式设置：

```text
ETHGUARD_ACCEPT_EXTERNAL_SIMULATE_GAP=1 ./scripts/release_check.sh
```

脚本只接受方法差异恰好为 `business missing: 1 [SimulateV1]` 的情况，运行其余 race、
构建、静态分析和漏洞门禁，并在成功输出中明确该结果**不是** `EthClient: complete`。
任何其他缺口都会使脚本失败。稳定兼容发布仍必须使用默认严格模式并关闭该缺口。
