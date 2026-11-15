# 工具与证据层

状态：现行（2026-11-04）。本文登记工具层各成员的业务职责、所有权与依赖方向，
并记录证据数据的保留策略。它不改变任何生产语义；核心运行时的边界由
`architecture_interlock_test.go` 与 `TestArchitectureDependencyInterlock` 执行。

## 依赖方向（唯一规则）

```
cmd/* 与 internal/{livecanary,shapeprobe,rpcsample}  ──导入──▶  核心运行时
核心运行时（root、rpc、rpc/methods、pool、aggregator、
internal/runtime、internal/reference、
config、observe、internal/transport、internal/typed） ──✗──▶ 工具/证据包
```

工具层是核心运行时的下游消费者。核心运行时反向导入任何工具或证据包都是
架构违规，由 interlock 测试锁定。`cmd/*` 本身不可被导入（Go binary），
interlock 只需约束 internal 三个工具包的反向边。

## 成员与所有权

| 工具 | 代码 | 业务职责 | 证据输出 |
| --- | --- | --- | --- |
| livecanary | `cmd/livecanary` + `internal/livecanary` | 对固定成员执行只读 plan，产出 SHA-256 溯源报告与不变量校验；观察而非 provider 正确性证明 | `docs/design/live-canary/` 仅 schema/合成示例；真实报告在 run artifact，不默认入库 |
| shapeprobe | `cmd/shapeprobe` + `internal/shapeprobe` | 对同一锚定高度的跨端点响应做字段级形状分类（Core/Extension/residual），回答「归一化没接受什么」 | `docs/samples/`（归档批次，见下） |
| rpcsample | `cmd/rpcsample` + `internal/rpcsample` | 按计划文件录制真实 RPC 样本（batch/index/records 三件套）并离线回放；`replay` 子包是 capture/golden 合同的离线验证器 | `docs/samples/`（归档批次）、`internal/rpcsample/testdata/`（被测试引用的活动样本） |
| transport contract | `pool/transport_contract_test.go` + `internal/transport/*` 契约测试 | pool 负责适配器选择与共同边界；HTTP L1 suite、WSS wire/lifecycle 与订阅 capability 各有独立契约，不能将单一 suite 外推为所有传输的等价证明 | 测试内嵌，无外部数据 |

其余 `cmd/*`（nativesmoke、soak、specbundle、specdiff、specmethod、wsscapture）是手动驱动
的运维/取证工具：soak 压测、nativesmoke 本机冒烟、specbundle/specdiff/specmethod 规格工具、
wsscapture WSS 抓包。它们没有计划进入默认 CI。

## 契约与 dated evidence 的归属

| 正文 | 唯一职责 | 不代表什么 |
| --- | --- | --- |
| [transport-contract.md](transport-contract.md) | HTTP L1 外部行为及 adapter 差异；§5 保留 2026-10-25 本机 native smoke | 本机 smoke 不等于外部兼容性认证 |
| [wss-wire-contract.md](wss-wire-contract.md) | 自有 WSS Upgrade/frame/连接、raw-call 与通知 demux 的 wire 边界 | 不定义订阅 quorum，不证明公开 provider 支持 |
| [subscription-contract.md](subscription-contract.md) | stream/source snapshot、聚合、Pull、预算、生命周期与现有实现差异 | 候选根裁决事件不是已导出 API |
| [wss-provider-evidence.md](wss-provider-evidence.md) | 2026-10-31/11-01 的真实 WSS presence、断连与未观察项 | 未观察到 null，四态 provider-backed golden 门仍未闭合 |
| [live-canary-contract.md](live-canary-contract.md) | HTTPS canary state/plan/artifact/recheck 契约；§12 保留 2026-10-23/24 观察摘要与 provenance | `inconclusive`、`repeatable=true` 或 artifact 上传成功不等于 passed |

完成阶段的 run 过程记录合并到以上 dated evidence，不再单独维护 stage-only 文件。
日期、样本数、原始产物定位/digest 与未关闭的证据缺口必须保留；阶段命令和执行
日记可由 Git 回看，不能把旧报告写成当前健康/发布状态。

## 证据数据保留策略

1. **活动样本**（被测试引用）：`internal/rpcsample/testdata/` 与
   `internal/livecanary/testdata/`。有测试引用即有归属；删除或移动必须连同
   引用它的测试一起评审。
2. **归档样本**（人工核对的历史证据）：`docs/samples/`，按
   `YYYY-MM-DD[-runN]` 命名，附同日 README 说明采集工具、端点与观察摘要。
   归档数据不参与任何测试断言；它存在的意义是让后续设计决策能回看当时的
   真实形状，不构成兼容性结论。
3. **未归属的原始落盘**：工具原始输出若进入代码树，必须有测试归属或归档
   说明；新输出的落点在采集时决定。已保留的真实 `docs/samples/` 原始证据不因
   目录整理、缺少测试引用或阶段完成而删除/合成替换。仓库外 capture 和 CI artifact
   可在 dated evidence 中保留定位与 digest，但摘要不能冒充原始数据或 attestation。
   `architecture_interlock_test.go` 不检查数据文件，数据归属靠评审纪律。
4. **禁止合成替代**：真实端点证据（含失败记录，如 HTTP 525、JSON-RPC -32000）
   不允许用合成数据替换后删除。证据门的关闭只能由新的真实采集完成。

## 默认 CI 边界

默认 CI 不执行真实 RPC 采集；livecanary / shapeprobe 的测试使用 testdata 与本地
httptest。依赖下载与 govulncheck 漏洞查询仍需联网。真实 RPC 采集只发生在显式运行或
`live-canary.yml`；发布流程在公开 Release 后调用相同 canary 工作流观察固定提交。

## 发布工具与产物清理

正式入口、工具版本与完整门禁见 [release-contract.md](release-contract.md)。
`scripts/release_build.sh` 调用 GoReleaser OSS 执行 pre-tag snapshot，校验固定的
15 个二进制并运行 5 个 Linux amd64 `-h` 入口；成功才删除本次 `dist`，失败保留。
`--keep-artifacts` 用于候选归档，`--verify-artifacts` 用于发布阶段只读复验。
`release-evidence/` 保存正式 API 报告与工作流证据；远端先归档后清理，不清 Go 缓存，
不再依赖临时 smoke 脚本、额外 worktree 或临时消费者工程。
