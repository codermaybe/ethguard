# T09 验证代码迁移前置契约

状态：**切片 A/B/C 已执行，测试函数名保持 TestT09 前缀**（2026-10-25）

本文是交给后续 agent 的工作边界。它不改变 T09 的方法矩阵、字段生命周期、
Decision、quorum、typed projection 或 reference 语义。

## 当前库存

T09 代码全部位于 `internal/verification/integration`，保持同一个
`ethguard_test` package。当前有 7 个文件、约 4,700 行和 45 个测试入口。
语义化重命名（切片 A）已应用，Git 按移动识别全部 7 个文件：

| 当前文件（语义名已应用） | 责任 | 原文件名 |
| --- | --- | --- |
| `method_scenarios_test.go` | T09.1 方法场景、请求形态和矩阵覆盖 | `t09_scenarios_test.go` |
| `scenario_harness_test.go` | 场景行执行器、Decision 维度断言 | `t09_matrix_helpers_test.go` |
| `verification_fixtures_test.go` | 链、交易、区块、fleet 和响应变体 fixture | `t09_fixtures_test.go` |
| `field_lifecycle_test.go` | 字段生命周期、表示稳定性和 typed round-trip | `t09_fields_test.go` |
| `protected_mutations_test.go` | 受保护字段 mutation/admission 边界 | `t09_mutations_test.go` |
| `evidence_conservation_test.go` | Value、Diagnostics 和每个 observation 的守恒 | `t09_conservation_test.go` |
| `typed_projection_test.go` | 生产 Client/ethadapter typed probes 和摘要 | `t09_typed_test.go` |

这些文件共同依赖同一组测试 fixture。第一轮只在当前 package 内移动/重命名，
不要把某个 helper 单独移到生产包，也不要为了目录整齐立即创建测试专用公共 API。

## 适合 Flash agent 的四个切片

### A. 语义化文件重命名

按上表使用 `git mv`，只改变文件名和文件头注释。先不改变 package、生产 import
或测试行为。完成后用 `git diff --find-renames --stat` 确认 Git 识别为移动。

### B. 测试辅助代码局部收敛

在同一个测试 package 内减少重复的场景断言、typed 摘要和 fixture 调用样式。
优化必须保持每个方法场景显式写出 status、participants、support、faults、
representative、Value 和 typed 结果；不能把场景矩阵重新隐藏到通用循环里。

允许改进命名、提取小型纯测试 helper、合并明显重复的断言；不允许引入第二套
typed decoder、从源码推导 expected value、缩小固定 fan-out 分母或改变错误断言。

### C. 按验证 seam 准备后续拆分

先记录每个 helper 的消费者，再决定是否进入未来的 `internal/testkit`。只有同时被
多个验证 Module 使用的 fixture 才有资格外提。当前 T09 fixture 主要服务 integration
package，不能仅因为文件较大就外提。

消费者清单已建立：[验证测试 helper 与 seam 库存](verification-helper-inventory.md)
（按符号级扫描记录调用者、职责与 seam 归属；结论：当前无可外提项）。

后续目录的意图是：

```text
internal/verification/contracts/   # 方法契约、编码、typed 一致性
internal/verification/scenarios/   # 决策场景和字段生命周期矩阵
internal/verification/integration/ # facade + pool + aggregator 联合验证
internal/verification/replay/      # capture/golden 离线回放
```

本切片只建立分类和依赖清单；如果拆包会迫使生产代码导出测试专用符号，应暂停并
把该项列为待审，而不是继续扩大 Interface。

### D. 验收门和文档同步

重命名或局部优化后同步测试入口说明和运行命令。保留 T09 专项门，直到所有测试
函数名也完成语义化迁移并相应更新命令；不要让现有验证命令静默失效。

每个切片至少运行：

```text
gofmt -w <changed-go-files>
go test ./internal/verification/integration -run 'TestT09' -count=1
go test ./... -count=1
go vet ./...
git diff --check
git diff --find-renames --stat
```

阶段收口仍需运行：

```text
go test ./... -race -count=1
go test ./internal/verification/integration -run 'TestT09' -race -count=2 -shuffle=on
```

## 执行记录（2026-10-25）

- 切片 A（`52eafd2`）：7 个文件按上表 `git mv`，Git 全部识别为 rename。
  唯一伴随代码变更是 `TestT091MatrixCoverage` 读取自身源码的两处路径字符串
  同步——该 AST interlock 按文件名解析本文件，重命名后必须同步，不构成行为
  变化。测试函数名保持 `TestT09` 前缀，专项命令继续有效。
- 切片 B（`4cacb51`）：package 内 helper 局部收敛——合并 Support/Reject/
  Dimension 来源断言，新增 decoded-value、fault-name、source-absence、
  whole-canonical 四个共享断言，删除重复的 `t09DecodeForMethod` 与等价的
  `t09Reorder`。场景字面量、固定基组、错误分类与生产 typed 路径未动。
- 切片 C（`ece2a46`）：helper/seam 库存文档建立（见上）。
- 维护者审计确认了上述切片与既有 `rpc/methods` 文档互锁失败
  （`TestRuntimeEvidenceDocsForbiddenClaims`：roadmap lost the T05 section）
  无关；该失败自 roadmap 重置提交起预先存在，另列任务处理，不混入 T09。

## 不变量和停止条件

后续 agent 可以重命名测试文件、局部 helper 和测试入口，但必须保持：

1. `Corroborated`、`Selected`、`Forwarded`、`Unknown` 的分类不变；
2. 固定 T09 基础 `N=5`、`H=2`、`Q=3` 和完整参与分母不变；
3. 失败、admission fault、normalization fault 和 diagnostics 的归属不变；
4. `Decision.Value` 只来自生产 projection，测试不能复制 typed 解码路径；
5. 普通测试不联网，不写入 capture、golden 或 testdata；
6. 任何需要改生产 Interface、quorum、方法 policy 或 reference 算法的发现都停止在
   当前切片，单独提交设计和实现。

## 已验证基线

前置库存完成时执行了：

```text
go test ./internal/verification/integration -run 'TestT09' -count=1
go test ./... -count=1
```

切片收口时复跑了完整验收：T09 专项（含 `-race -count=2 -shuffle=on`）、
`go test ./... -race -count=1`、`go vet ./...`、`go fmt`、`git diff --check`
全部通过；全量测试仅失败于上文记录的既有 T05 文档互锁（与 T09 无关）。
本文之后的变更应把 T09 专项命令的结果作为比较基线。
