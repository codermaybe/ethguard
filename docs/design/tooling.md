# 工具与证据层

状态：现行（2026-11-04）。本文登记工具层各成员的业务职责、所有权与依赖方向，
并记录证据数据的保留策略。它不改变任何生产语义；核心运行时的边界由
`architecture_interlock_test.go` 与 `TestArchitectureDependencyInterlock` 执行。

## 依赖方向（唯一规则）

```
cmd/* 与 internal/{livecanary,shapeprobe,rpcsample}  ──导入──▶  核心运行时
核心运行时（root、rpc、rpc/methods、pool、aggregator、
internal/runtime、internal/reference、
config、observe、gethcompat、internal/transport、internal/typed） ──✗──▶ 工具/证据包
```

工具层是核心运行时的下游消费者。核心运行时反向导入任何工具或证据包都是
架构违规，由 interlock 测试锁定。`cmd/*` 本身不可被导入（Go binary），
interlock 只需约束 internal 三个工具包的反向边。

## 成员与所有权

| 工具 | 代码 | 业务职责 | 证据输出 |
| --- | --- | --- | --- |
| livecanary | `cmd/livecanary` + `internal/livecanary` | 定期对真实端点做只读 Canary 采集，产出带 SHA-256 溯源的对比报告与不变量校验；是「真实兼容性证据门」的采集器 | `docs/design/live-canary/`（schema 与示例）；报告按运行落盘，不入库默认路径 |
| shapeprobe | `cmd/shapeprobe` + `internal/shapeprobe` | 对同一锚定高度的跨端点响应做字段级形状分类（Core/Extension/residual），回答「归一化没接受什么」 | `docs/samples/`（归档批次，见下） |
| rpcsample | `cmd/rpcsample` + `internal/rpcsample` | 按计划文件录制真实 RPC 样本（batch/index/records 三件套）并离线回放；`replay` 子包是 capture/golden 合同的离线验证器 | `docs/samples/`（归档批次）、`internal/rpcsample/testdata/`（被测试引用的活动样本） |
| transport contract | `pool/transport_contract_test.go` + `internal/transport/*` 契约测试 | 固定 HTTP/WSS/geth 三个适配器必须共同满足的传输合同（超时、关闭、错误分类）；所有权归 pool 的适配器选择测试与 transport 包自身 | 测试内嵌，无外部数据 |

其余 `cmd/*`（nativesmoke、soak、specbundle、specdiff、wsscapture）是手动驱动
的运维/取证工具：soak 压测、nativesmoke 本机冒烟、specbundle/specdiff 规格工具、
wsscapture WSS 抓包。它们没有计划进入默认 CI。

## 证据数据保留策略

1. **活动样本**（被测试引用）：`internal/rpcsample/testdata/` 与
   `internal/livecanary/testdata/`。有测试引用即有归属；删除或移动必须连同
   引用它的测试一起评审。
2. **归档样本**（人工核对的历史证据）：`docs/samples/`，按
   `YYYY-MM-DD[-runN]` 命名，附同日 README 说明采集工具、端点与观察摘要。
   归档数据不参与任何测试断言；它存在的意义是让后续设计决策能回看当时的
   真实形状，不构成兼容性结论。
3. **无引用落盘**：任何工具输出落入代码树却没有测试引用、也没有归档 README
   的，视为未处理数据——要么移入 `docs/samples/` 补 README，要么删除。
   `architecture_interlock_test.go` 不检查数据文件（数据归属靠评审纪律），
   但 topology 文档的目录地图与本文是检查清单。
4. **禁止合成替代**：真实端点证据（含失败记录，如 HTTP 525、JSON-RPC -32000）
   不允许用合成数据替换后删除。证据门的关闭只能由新的真实采集完成。

## 默认 CI 边界

默认 CI（`ci.yml`）不含任何真实网络调用：livecanary 与 shapeprobe 均为手动
触发的离线可测工具（`internal/livecanary` 的测试使用 testdata 与本地
httptest，不访问外网）。真实网络采集只发生在手动运行或
`live-canary.yml` 的显式工作流中。
