# Release Contract（发布门禁与发布契约）

状态：**现行（2026-11-09）**。本文件冻结发布入口、前置条件、门禁覆盖范围、产物
与发布语义、失败处置，并登记本轮实际证据。业务完成度见
[project-roadmap.md](project-roadmap.md)；本文件不维护第二份业务计划或状态台账。

关联：`decisions.md` 0057（外部消费者前提）、[tooling.md](tooling.md)（工具层所有权）、
[live-canary-contract.md](live-canary-contract.md)（canary 证据分层）、
`.github/workflows/{ci,release,live-canary}.yml`、`.goreleaser.yaml`、
`scripts/{release_check,release_api_diff,release_build,govulncheck}.sh`。

---

## 1. 发布入口与前置条件

- **发布入口**：在 `master` 手动运行 `.github/workflows/release.yml`，仅输入新版本号；
  不再以推送 tag 触发发布。工作流冻结该次 dispatch 的完整 commit SHA，确认它仍是
  `master` HEAD，然后从正式 checkout 执行验证；不能用未提交的本地结果替代。
- **版本**：`vMAJOR.MINOR.PATCH`，可带预发布标识；数字标识不允许前导零。
  含 `-` 的版本标记为 prerelease 且不设为 Latest。现有 tag / Release 一律拒绝覆盖。
- **保护**：仓库必须先配置 `release` Environment，至少一个 required reviewer，
  禁止发起人自审，并只允许 `master` 部署。准备阶段检查这些条件，未配置即失败；
  不能依赖 GitHub 自动创建的空 Environment 充当保护。
- **权限**：准备阶段只有 `contents: read`；唯一 `contents: write` job 位于受保护
  Environment 之后。审批前只生成 Actions artifact，不创建 tag、draft 或公开 Release。
- **顺序**：固定 SHA → 质量门禁、API 报告、fuzz、构建与 smoke → 不可变候选 artifact
  → 人工审批 → 下载并复验相同产物 → 创建 tag 和 draft → 上传与远端校验 → 公开 Release。
  发布 job 不重新编译；审批者需审阅 API 报告、提交及 notes。

## 2. 正式入口与门禁覆盖

| 入口 | 作用 | 输出与限制 |
| --- | --- | --- |
| `bash scripts/release_check.sh` | 格式、tidy、公开生成类型新鲜度、vet、build、全仓 race、可达漏洞扫描 | 不是离线流程；需要 `vuln.go.dev`；不代表完整发布验证 |
| `bash scripts/release_api_diff.sh PREVIOUS_TAG` | 上一已发布版本与当前整模块公共 API 对比，包含删除的包 | `release-evidence/`；API 变化仅报告，工具、下载与导出错误失败 |
| `bash scripts/release_build.sh` | GoReleaser snapshot、15 个产物校验、5 个本机入口 `-h` smoke | 成功才删除新建的 `dist`；失败保留；拒绝覆盖原有目录 |
| `bash scripts/release_build.sh --keep-artifacts` | 相同构建验证，但保留候选产物 | 工作流在归档后清理 |
| `bash scripts/release_build.sh --verify-artifacts` | 只读校验固定 15 项的文件集合与 SHA-256 | 不要求 GoReleaser，不运行或删除二进制 |
| `release.yml` prepare | 以上入口，加 jsonsafe / wire 各 100 万次 race fuzz | 全部完成后才允许进入发布 job |
| `ci.yml` release-config | GoReleaser schema、shell 语法、actionlint | push / PR 的配置门；不创建产物或发布 |

本地构建入口要求 Linux amd64、Bash 4+、Go、GoReleaser、`sha256sum`；API 入口要求
Go 和 jq。工作流使用 Ubuntu runner，另使用预装的 gh、jq 和 tar。
本地脚本验证当前工作树，不声称绑定提交；发布工作流另行检查 SHA 与 checkout 清洁性。
`release_check.sh` 与 CI 的独立矩阵允许重复，但门禁语义必须同步。

Go 1.26 的 duration fuzz 存在到期误报竞态（[golang/go#75804](https://github.com/golang/go/issues/75804)，
[backport / 计数预算说明](https://github.com/golang/go/issues/82363)）；不忽略或重试该错误，
改为每目标 `-fuzztime=1000000x -parallel=2 -timeout=10m`，两目标串行执行；
真实反例、引擎错误和超时仍失败。

## 3. 工具链与固定版本政策

- 项目工具链来自 `go.mod` 的确切 go patch 指令；当前为 `go1.26.10`。
  本地 `GOTOOLCHAIN=auto` 可下载工具链，`actions/setup-go` 后的 `local` 使用已安装版本。
  GoReleaser 自身的构建 Go 版本不改变项目二进制的编译工具链。
- 禁止 `@latest`；固定工具仍需满足项目工具链下限。当前引用：

| 工具 | 版本 | 引用点 |
| --- | --- | --- |
| GoReleaser OSS | `v2.20.0` | `.goreleaser.yaml`、`release_build.sh`、CI / release workflow |
| goreleaser-action | `v7.3.0`，固定 commit SHA | CI / release workflow，仅安装工具 |
| actionlint | `v1.7.12` | `ci.yml` release-config |
| apidiff | `v0.0.0-20261014143641-f0251eb4aa3e` | `release_api_diff.sh` |
| govulncheck | `v1.9.0` | `govulncheck.sh` |
| deadcode | `v0.41.0` | CI 定时 job |
| golangci-lint | `v2.15.0` | CI |

GoReleaser 使用官方 schema 和默认构建隔离目录；显式保留 `-trimpath`、`-s -w`，
不注入不存在的版本变量、不覆盖 CGO。snapshot 不创建 tag、不执行发布。
工具参考：[GoReleaser Quick Start](https://goreleaser.com/getting-started/quick-start/)、
[Go builds](https://goreleaser.com/customization/builds/go/)。

## 4. 产物与发布语义

- `cmd/{soak,rpcsample,shapeprobe,specbundle,specdiff}` ×
  `{linux_amd64,linux_arm64,darwin_arm64}`：15 个原始二进制与 `checksums.sha256`。
  资产命名与原流程保持一致；GoReleaser metadata 不上传为 Release 资产。
- 候选 artifact 保存 payload tar、固定 SHA / 版本 / 前版 / run ID 的 manifest、API
  导出与报告、门禁和构建日志、notes、证据校验和，保留 14 天。payload 内保存文件权限。
  准备阶段成功归档后删除本地产物；失败保留并上传现有诊断。不会清理 Go 缓存。
- 发布 job 复验 manifest 与 payload，再创建不可移动的 annotated tag 和 draft。
  上传后通过 GitHub API 确认恰好 16 个资产，下载并校验各文件 SHA-256，再公开 Release。
  准备与发布清理均不删除远端 artifact；发布失败保留本地诊断与现有 tag / draft。
- tag 一旦创建，模块代理即可取用；GitHub Release 公开前仍可能存在短暂 tag-only
  窗口。不能保证远端 API 永不失败，也不自动回滚或覆盖已公开内容。
- `GITHUB_TOKEN` 创建 tag 不触发另一条 push 工作流；release 在发布后显式调用可复用
  live-canary，绑定同一 SHA / tag。预发布的 `inconclusive` 只表示观察不足，`failed`
  仍失败；稳定版本严格执行 canary 契约。canary 是发布后的独立观察，不回滚发布。
- 历史 beta.2 的 `prerelease=false` 与 beta.3 tag-only 状态不变。

## 5. 证据边界与失败处置

本地 `--snapshot` 能证明配置、编译、checksum 和入口运行，不能证明 GitHub 权限、
Environment 审批、远端资产上传或 CDN 行为。远端发布成功必须另附实际 workflow run。
准备失败可在修复后的新提交重新 dispatch；创建 tag 后失败按第 7 节处理，不移动 tag。
整模块 API 差异是审批输入，不是兼容性通过证明；预发布 notes 必须保留当前外部兼容
缺口。稳定发布仍需默认严格方法门禁通过。

## 6. 日期化发布证据

以下是已发生的诊断和观察，不是本次文档清理重新执行的检查，也不替代当前 SHA 的发布门禁。完整日志与实施过程留在 Git／对应 workflow。

| 日期／版本 | 可保留的事实与教训 | 证据边界 |
| --- | --- | --- |
| 2026-11-04 beta.1，`c339b5f` | release run `41825635461` 在 apidiff 安装失败；`@latest` 工具要求 Go1.26，而当时锁 Go1.25.3 并强制 local | 不是 govulncheck 在 release 中失败；同日定时 CI 的 scanner／deadcode 失败属于同类工具链下限问题 |
| beta.1 canary run `41825635367` | provider 观察 inconclusive，当时 tag gate 因此失败 | workflow failure 不等于观察证明了业务错误 |
| Go 工具链提升 | 旧本地副本 Go1.25.3 曾报告30个可达标准库漏洞；GO-2026-6809 修复版本是 Go1.26.10 | 升级有可达安全依据；旧扫描的零可达结论不代替以后每次正式 scanner |
| beta.2，`b6e7829` | release `41829485059`、canary `41829485063` 成功，16资产；相对 beta.1 仅 workflow／文档变化，模块 API 相同 | beta.1 保留 tag-only，不删除或补发；这些历史状态不改写 |
| 2026-11-09 GoReleaser 接入 | 本地15项构建／checksum、5入口 smoke、候选打包解包／只读复验、正式 API 差异与质量脚本曾通过 | 没有执行远端 Environment 审批／上传或真实 RPC canary，不宣称 EthClient complete |
| 同批 fuzz 诊断 | duration 到期错误未生成反例；8-worker 最小化 EOF 根因未定，改为第2节计数预算、2-worker串行 | 不忽略引擎错误、不减少目标执行次数；参数与当前入口以第2节为准 |

beta.3 的脚本执行位失败见下一节。失败 tag 的不可变性与 SHA／产物绑定，是保留这些历史摘要的原因；阶段完成清单、逐条本地命令输出不再复制成当前规范。

## 7. 失败处置

1. tag 运行失败时**不改写、不删除、不移动**该 tag（模块代理可能已缓存），也**不重跑**
   同一 run（工作流取自该提交，结果相同）。
2. 修复后打新的 tag；失败 tag 的最终处置（补 Release / 作废）由主维护者决定，
   默认保留现状，并在下一个 Release notes 中说明。
3. 发布产物 attach 之后视为已发布证据，不得替换 `dist` 内容。
4. 任何"已通过"的记录必须写清执行范围（第 2 节）、工具链（第 3 节）和未观察项。

### beta.3 发布失败：脚本执行位（2026-11-09）

`v0.0.1-beta.3` 指向 `e3de095`；[release run 42501751526](https://github.com/codermaybe/ethguard/actions/runs/42501751526)
在 preflight 直接报 `./scripts/release_check.sh: Permission denied`（exit 126），
未进入 Go 检查，也未生成 GitHub Release。Git 中该脚本记录为 `100644`，本机
`core.fileMode=false`，本地执行成功不能证明干净 checkout 具有执行位。
发布入口改为显式 `bash` 调用，与现有 govulncheck 脚本调用约定一致；不依赖执行位。
已推送的 beta.3 tag 保留不变，不将本地门禁通过表述为该 tag 的远端发布成功。
修复验证在新建的干净 worktree 中执行：确认脚本没有任何执行位后，通过更新后的
`bash ./scripts/release_check.sh` 命令运行预发布门禁；race、build、vet、tidy 和
govulncheck 均通过。此为修复后的本地证据，不改变 beta.3 的远端失败结论。

## 8. 外部兼容缺口的显式交付模式

当前锁定的 go-ethereum `v1.17.10` 在默认根包架构下无法把官方
`SimulateV1` 接入 `EthClient`；该事实由兼容性政策记录，不能被中间方法集合掩盖。
默认 `release_check.sh` 仍以 `ETHGUARD_REQUIRE_COMPLETE=1` 执行并拒绝该缺口。

在本轮已接受该外部边界、但仍需完成代码与安全门禁时，可以显式设置：

```text
ETHGUARD_ACCEPT_EXTERNAL_SIMULATE_GAP=1 bash ./scripts/release_check.sh
```

脚本只接受方法差异恰好为 `business missing: 1 [SimulateV1]` 的情况，运行其余 race、
构建、静态分析和漏洞门禁，并在成功输出中明确该结果**不是** `EthClient: complete`。
任何其他缺口都会使脚本失败。稳定兼容发布仍必须使用默认严格模式并关闭该缺口。
