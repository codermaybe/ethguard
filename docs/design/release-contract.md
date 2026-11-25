# Release Contract（发布门禁与发布契约）

状态：**2026-11-25 质量门禁更新，现行发布流程**。本文件冻结发布入口、前置条件、门禁覆盖范围、产物
与发布语义、失败处置，并登记本轮实际证据。业务完成度见
[project-roadmap.md](project-roadmap.md)；本文件不维护第二份业务计划或状态台账。

关联：`decisions.md` 0057（外部消费者前提）、[验证与证据合同](method-verification-pipeline.md#8-工具所有权与材料保留)（工具层所有权）、
[live-canary-contract.md](live-canary-contract.md)（canary 证据分层）、
`.github/workflows/{ci,release,live-canary}.yml`、`.goreleaser.yaml`、
`scripts/{lint,release_check,release_api_diff,release_build,govulncheck}.sh`。

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
| `bash scripts/release_check.sh` | 格式、tidy、公开生成类型新鲜度、vet、固定版本默认/anvil lint、anvil纯编译、build、全仓 race、可达漏洞扫描 | 不是离线流程；需要 `vuln.go.dev`；不代表完整发布验证 |
| `bash scripts/lint.sh` | 固定golangci-lint 2.15.0，默认与anvil全仓扫描 | 生产、测试、cmd/testkit、生成器与生成代码全部纳入；没有本地链或provider请求 |
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
发布与定时fuzz均采用每目标 `-fuzztime=1000000x -parallel=2 -timeout=10m`；发布两目标、定时十一目标分别串行执行，发布仍带race；
真实反例、引擎错误和超时仍失败。

## 3. 工具链与固定版本政策

- 项目工具链来自 `go.mod` 的确切 go patch 指令；当前为 `go1.26.11`。
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
| golangci-lint | `v2.15.0` | `scripts/lint.sh`，CI及release_check共用 |

GoReleaser 使用官方 schema 和默认构建隔离目录；显式保留 `-trimpath`、`-s -w`，
不注入不存在的版本变量、不覆盖 CGO。snapshot 不创建 tag、不执行发布。
工具参考：[GoReleaser Quick Start](https://goreleaser.com/getting-started/quick-start/)、
[Go builds](https://goreleaser.com/customization/builds/go/)。

### 静态检查覆盖与例外

`lint.sh`复用PATH中版本匹配的二进制，否则用当前Go工具链隔离安装固定版本；`GOLANGCI_LINT`可指定已有二进制，但版本不符直接失败。安装目录在退出时删除，不修改业务go.mod/go.sum。两次扫描均使用仓内`.golangci.yml`，任一失败即失败；不能只查diff或遗漏anvil。二进制自身构建Go与分析器也必须支持项目语言版本，版本字符串一致不替代实际扫描通过。

配置显式开启tests、关闭generated排除、清空路径和预设排除，诊断不按数量截断或按行去重。原14个linter加wastedassign、unconvert、nolintlint形成17个阻塞检查器。固定2.15.0实际`linters --json`显示Enabled=17、Disabled=98，配置为default: standard，不是default: all。Staticcheck的checks: all仅指该分析器内部检查，仍排除六项ST规则及QF*；不能理解为golangci全部规则已开启。nolint必须具名、具因且实际使用；errorlint.errorf=false保持class sentinel与detail的既有错误链合同。errcheck对fmt.Print类和显式空标识符有自身规则范围，不把零告警等同于检查了每次I/O；主报告写出另有失败writer反例。

| 审查型规则 | 已完成的判断与持续使用方式 |
| --- | --- |
| G115 | 修复实际数值/预算风险、九条测试窄化；剩余转换依据正数准入、帧高位/长度档位、关闭码、len及签名BitLen证明，固定fixture单独核对；保持全仓报告，不新建整测试豁免 |
| forcetypeassert | Logs和canonical传递既有typed事实；Simulate两个冻结RawMessage断言有构造证明，其余为fixture、明确resolver能力或录制值断言；不批量增加包装helper |
| gocritic | 两处exitAfterDefer已处置，三个解码return显式排序；租约append、有限fixture defer及重复来源回调保留；hugeParam/rangeValCopy须结合快照隔离和性能证据 |
| gocognit/gocyclo/dupl/goconst/mnd | 生成、测试与生产按owner区分审查；不以统一阈值推动拆分、指针化或全局constants包。mnd内部默认不查测试等，报告需注明范围 |

结构保留理由见[代码拓扑](codebase-topology.md#可读性与结构诊断的处置2026-11-25)。报告规则未启用为阻塞门，不代表被忽略或被判为已修复；后续变化须重新评估原范围证明。静态扫描anvil加纯编译只证明该构建路径可分析、可编译，不证明链上交易执行。

### 后续Go 1.27迁移

独立固定目标patch、lint tag及其构建Go和分析器支持；不使用@latest或仅从issue关闭推断兼容。以当前1.26工具链、1.27编译但保留go.mod语言基线、再提升语言基线三步比较，区分工具新增诊断与项目缺陷。相同测试/race、生成/JSON保真、成本和平台门槛通过后才同步CI与本地门禁。modernize仅逐条审查，不顺带更换JSON库、生成器、别名或取消语义。本轮未迁移Go1.27；最终漏洞扫描后，经用户明确授权追加Go1.26.11及最小依赖安全升级，见第6节。

### 质量报告的重建与处置口径

2026-11-25源码治理冻结后的Go1.26.10/lint2.15.0全仓报告如下；默认60包，anvil62包。安全补丁升级不改项目Go源码，最终阻塞门另在Go1.26.11重跑。报告扫描退出1表示存在下列诊断，不能记成零告警，也不能与加载/typecheck失败混淆。

| 规则 | 原基线默认/anvil | 治理后默认/anvil | 处置 |
| --- | --- | --- | --- |
| wastedassign / unconvert | 1/1、37/37 | 0/0、0/0 | 确定冗余已删除，升为阻塞门 |
| G115 | 42/42 | 33/33 | 13项位于非_test文件（含testkit），20项测试；保留处已审范围证明，实际预算/窄化风险已有反例与修复 |
| forcetypeassert | 172/172 | 168/168 | 2项Simulate构造证明、166项测试；canonical与Logs四处类型事实不再丢失 |
| gocritic | 603/604 | 601/602 | exitAfterDefer由2变0、evalOrder由6变3；其余高风险候选按所有权/fixture审查，复制建议不机械改指针 |
| gocognit / gocyclo | 410/413、354/359 | 414/417、357/362 | 显式错误出口及预算检查增加分支，接受可读的局部检查，不为阈值再拆层 |
| dupl / goconst / mnd | 29/29、1556/1556、282/291 | 29/29、1555/1555、278/287 | 已按模块、协议/资源/政策常量审查，数值不是待修缺陷计数 |

重建时从仓内配置复制到临时目录，保持tests=true、generated=disable、空路径/预设排除和三个issues数量选项。正确性组用default=none，仅启用ineffassign/wastedassign/staticcheck/unconvert/forcetypeassert/gosec，gosec.includes=[G115]，Staticcheck仍使用仓内checks；结构组仅启用gocritic/gocognit/gocyclo/dupl/goconst/mnd，gocritic.enabled-tags=[diagnostic,performance]，gocognit.min-complexity=20、gocyclo.min-complexity=15、dupl.threshold=150、goconst.min-len=3及min-occurrences=3。分别运行全仓默认/anvil，保留JSON与工具退出状态。mnd自身默认不检查测试等限制仍须注明；不能把配置tests=true解释成每条分析器都检查所有测试表达式。

当前83条nolint均具名具因且经nolintlint核对使用；不新增整目录/全测试豁免。工具和fixture清理只有确认无待提交输出时才显式丢弃错误；正常输出有失败writer反例。静态报告的具体保留理由归[代码拓扑](codebase-topology.md#可读性与结构诊断的处置2026-11-25)，不复制为新的阶段清单。

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

### 2026-11-25最小安全升级与本地复验

质量源码冻结后，Go1.26.10上的全仓普通/race各52个有测试包、7096个测试通过事件完成，另8包无测试；没有失败或测试跳过。既有govulncheck1.9.0随后报告3项可达漏洞并exit3，历史零漏洞结论不能继续作为发布依据。用户明确授权把最小安全升级加入本轮。

| 扫描报告 | 固定修复与范围判断 |
| --- | --- |
| GO-2026-6859 | Go1.26.10→1.26.11；官方触发涉及HTTP/1服务端MaxBytesReader超限及并发读body/写header。本仓未发现MaxBytesReader/Handler调用；Client.Do等保守调用图轨迹不等于已复现服务端竞态 |
| GO-2026-6974 | OTel trace1.46.0→1.47.0，依赖图同步core/metric并引入log1.47.0；修复合法U+FFFD绕过属性长度限制。auto/sdk1.2.1保持；本仓无直接OTel配置/属性调用，仍消除宿主接入instrumentation时的依赖风险 |
| GO-2026-6278 | Gorilla1.5.3→v1.5.4-0.20240701034025-d67f41855da4；原生WSS已使用crypto/rand且不链接Gorilla，修复实际覆盖geth oracle/nativesmoke可选WS客户端。扫描的sync.Once/Close路径不证明本仓生成弱掩码 |

Gorilla修复下界含14个提交，附带代理context、子协议优先级及Upgrade/Hijack行为变化；OTel1.47也不是单行补丁，均按依赖变化验收。x/net0.59.0是原模块图已选版本，新Gorilla直接使用其proxy包后进入go.mod；其模块要求使go.sum中的x/crypto选择0.57.0。go-ethereum仍为1.17.10，lint仍为2.15.0并用Go1.26.11重建；没有升级Go1.27。另修正基线receipt测试直接引用uint256的require分类，uint256版本不变。

升级后固定scanner已exit0：**0个可达漏洞、0个已导入包漏洞**；仍报告7个仅require模块层漏洞，当前代码未调用。不宣称全部传递依赖零漏洞，也不为未导入包扩张本次最小升级。最终工作树已冻结验收，1211个非Markdown文件在验证前后哈希一致。

| 最终本地检查（Go1.26.11，linux/amd64） | 结果与范围 |
| --- | --- |
| 全仓普通、全仓race | 各52个有测试包、7096个测试通过事件（含子测试），另8包无测试；0失败、0实际测试跳过。均设置ETHGUARD_REQUIRE_COMPLETE=1、-p 2、-count=1、-timeout=240s |
| 静态/构建 | go vet、go build、tidy、gofmt、diff检查、生成Go/audit逐字节一致；17规则默认/anvil各0问题，anvil62包纯编译通过 |
| 平台 | linux/arm64、darwin/arm64、windows/amd64交叉build及Windows交叉vet通过；没有这些平台的本机运行证据 |
| 配置 | 所有scripts/*.sh的bash语法及固定actionlint1.7.12通过；新lint脚本的版本拒绝及两次扫描失败传播分别验证 |
| 帧输入 | FuzzFrameStream在2个worker下完成100万次，普通fuzz通过；不是race fuzz，也不替代全部11个定时目标或发布两目标 |
| 方法表面 | 锁定geth1.17.10为48个receiver方法，除Client访问器外47项均有对应入口；方法集合与项目签名测试普通/race通过。兼容范围见方法覆盖清单 |

最终普通/race日志中下列27项代表性流程、固定分母、内部故障归因与长期预算测试全部通过；成功、失败/取消、实际退出分别核对，不能用Next返回终态代替Close完成屏障。

| 流程 | 代表性成功 / 失败或取消 / 完成屏障 |
| --- | --- |
| Read | facade.TestCallResultCompositeReadCarriesReferenceOnSuccess / TestCallResultCompositeReadKeepsEstablishedReferenceOnBusinessFailure、TestUserCancellationKeepsItsOwnCause / TestCloseConvergesInFlightBackgroundRead |
| 同步写入 | integration.TestSyncWriteReceiptSuccess / TestSyncWriteUnconfirmedBroadcastNeverPolls、TestSyncWriteCancellationDuringBroadcastAndReceipt / TestSyncWriteOwnerClosePreservesEarlierObservation |
| Head | facade.TestSubscribeNewHeadPublicEntrypointsUsePathWitnessesAndHashRecovery / root.TestHeadProducerFailureDrainsMultipleDeltasAndCloseReleases / TestHeadCloseJoinsCancelledLookupBeforeReturning |
| Logs | facade.TestSubscribeFilterLogsPublicEntrypointsProveNonemptyBlock / root.TestLogsStreamTerminalVisibleBeforeHungSupplierJoin / TestLogsStreamSlowMinorityDoesNotDelayDeliveryButCloseJoins |
| Receipts | facade.TestSubscribeTransactionReceiptsPublicEntrypointsFollowHeadAndFreezeFilter / root.TestReceiptsBudgetFailuresDoNotCommitOrLoseAudit / TestReceiptsNextCancellationAndConcurrentCloseJoinActualWork |

其余10项核对pool/WSS关闭拒绝新工作、canonical无snapshot固定Q与内部错误不罚provider、receipts固定分母、事件租约成功/失败转移，以及Head/Logs/Receipts长期保留和归零。有限race与本地退出不证明全部调度或远端服务端资源已终止。

额外Gorilla验证中，本仓oracle/诊断/互操作/依赖边界普通与race各124测试事件通过，geth WebSocket取消/Ping普通2项通过。上游定向16项为14通过、2失败；同Go1.26.11的旧v1.5.3两项同样失败。失败的TestProxyDial/TestProxyAuthorizationDial夹具只写CONNECT 200而不Hijack/建立隧道，net/http随后关闭连接，不能记为上游全通过或新版本回归。隔离临时module的真实CONNECT契约改用Hijack及双向转发：WS/WSS×无认证/Basic认证四组合检查目的、认证不泄漏到origin、101/正文往返、单连接计数及关闭后两侧任务退出；另分别验证代理NetDialContext取消及实际CONNECT等答deadline关闭socket。新普通、新race、旧普通各7测试事件通过，主控已读源码与结果；没有声称无deadline的CONNECT等答阶段即时取消。

本轮只完成当前工作树的质量治理与最小安全升级，没有执行真实Anvil链、外部provider、全部发布fuzz、GoReleaser产物构建/检查或远端CI/发布。未运行完整发布脚本来生成候选产物；其质量检查组成项已按上述范围独立执行。build出现已知模块stat缓存只读警告但exit0，保留记录。日志`/tmp/ethguard-security-*`及临时代理测试仅作补充，长期恢复依据本节、仓内测试与固定脚本，不依赖临时文件继续存在。用户随后授权按改动分组本地提交：数值/资源7908ab5、类型事实59fae6b、错误/测试d86b903、lint/CI bc1bf17、安全升级7493d0d；本文与其他正式文档一并单独保存。未推送。

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

## 8. 方法集合与语义验收

`release_check.sh` 始终以 `ETHGUARD_REQUIRE_COMPLETE=1` 检查锁定版本的完整业务方法
集合，不接受缺失 `SimulateV1` 的预发布豁免。方法数闭合只证明 facade 方法名集合符合
目标；它不证明各方法的官方字段、错误分类、reference、跨传输、多源裁决、原始证据
与生命周期合同已逐项验收。稳定发布还须核对 [逐方法覆盖清单](method-transport-coverage.md)
中的未完成证据，并完成版本发布审查。`SimulateV1` 已使用生成的请求与 selector，
这不替代逐方法模拟语义、错误分类和外部 provider 证据验收。
