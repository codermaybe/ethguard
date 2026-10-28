# Next-Phase Implementation Report（实施报告，供统一验收）

> 状态：已完成（2026-10-29，分支 `next-phase-architecture`）。本报告逐任务列出
> 提交、交付物与门禁证据，供统一验收（GPT 复核 + 人工验收）比对。提案
> `next-phase-architecture-proposal.md` 的任务表为本报告的验收清单。

## 1. 任务与提交对照

| 任务 | 提交 | 交付物 | 门禁证据 |
| --- | --- | --- | --- |
| P0 原则冻结 | `60175f3` | `docs/design/next-phase-decisions.md`（16 项决定 D1–D16） | 决策时机表逐项落地；推翻修订记录留白待验收 |
| S0 值模型 | `aafe00c` | `rpc/basis.go`、`rpc/invocation.go`、`rpc/outcome.go` + 3 个测试文件 | 不可变/Clone/单次 WithReference/并发读测试；`-race` 通过 |
| S1 门槛语义 | `f207c6b` | `docs/design/acceptance-thresholds.md` + 双表驱动测试 | admission 与 establishment 公式逐场景锁定；合并决策：保留两套（理由见文档 §3） |
| S2 Outcome 兼容层 | `8e49001` | strict 的 ResolveOutcome(+Corroborated)、6 个孪生转发、aggregator verdict 投影 | 五节点 fleet 端到端等价；gated 成员 absent 重分类；verdict 存在时不二次 Normalize |
| R0 Reference 拆分 | `d799fd8` | HeadReader/HeadCodec/HeadView 归 reference；runtime PoolHeadReader + MethodHeadCodec；退役 Custom/confirmedAnchor | reference 不 import pool/methods/aggregator（interlock 钉住）；Basis 驱动 reader 测试 |
| T0 contract harness | `e2fe536` | `internal/transport/contract`（16 条 L1 子测试）+ `docs/design/transport-contract.md` + 中立 ErrConnectionClosed | native 全套通过 |
| R1 ReferenceStage | `c30d357` | runtime StrictHead/FinalizedHead 服务 + ReferenceStage 实现 + rpc.ReferenceKind/Request | 旧输出/错误/ObservedAt/evidence 等价（verification 套件全绿） |
| P1–P5 纸上原型 | `392b71c` | `docs/design/prototypes/`（总览 + 5 份原型） | 门禁检查表全过；无生产编译器代码 |
| R2 单 Invocation 编排 | `7915df5` | aggregator 命名阶段（Prepare→ReferenceStage→PrepareWithReference→Collect→Admit→Resolve→Project）+ Invocation 贯穿 | trace fixture：一个 ID/一个 Basis/一次建立/无嵌套 Aggregate/建立失败业务 fan-out 为零/成员变化基不变 |
| R3 组合根收尾 | `61ed891` | root anchor 全转发 runtime 服务；tag 路由合并；topology 文档 Reference/编排节修订 | `rg aggregator.New` 生产仅 internal/runtime |
| R4 声明式 Reference | `83e6c65` | rpc.ReferenceRequirement + 四个锚定层声明 + aggregator 拥有建立（哨兵协议） | moving-tag 矩阵不改即通过；Validate-before-Anchor 保持；方法不再持有网络回调 |
| R5 reference 字段冻结 | `7f152ee` | `docs/design/method-spec-reference-freeze.md`（字段语义 + 23 方法 inventory） | 每方法显式值或 none |
| L0 拆包 | `4e50768` | internal/transport Port-only；geth adapter 迁出；pool 去 alias/去 geth sentinel；默认 native | `go list -deps .` 无 ethclient |
| L1 gethcompat | `4e50768` | gethcompat 显式构造 seam + root DialerOption（geth 选择报错指向 seam） | native root 与 gethcompat 均可构造；公共签名不随构建变化 |
| L2 interlock | `8e402e4` | 架构 interlock 扩展（ethclient 唯一导入点、gethrpc 限界、root 禁 geth）+ TestDefaultBuildExcludesEthclient + CI link-time job | 默认与兼容双通道门禁进 CI |
| G0 geth 对齐 | `c3ac12d` | geth adapter 过同一 L1 suite：Close 生命周期、退化 error、空/坏 body 哨兵、版本/trailing data 严格化 | 偏差表仅剩 L2/L3（诊断文本、body 保留、位置匹配 id、WSS trailing data） |
| M0 MethodSpec 编译器 | `5ac67f4` | spec 类型 + 冻结 inventory（23 方法）+ Compile + 能力镜像包装 | 全方法等价测试：rule 投影/能力集/声明/Prepare/Resolve 逐项相同 |
| M1 组合根切换 | `301b135` | runtime.Build 从 Specs()+Compile 装配注册表 | 全部 fleet 级场景（矩阵语义/verification/livecanary）走编译链不变通过 |
| V0 catalog/interlock | 本提交 | Catalog 增 Family/Coverage/Alignment + 目录 interlock 测试 | registered⇔spec 双向锁定；eth_getProof 显式 unsupported verification |

## 2. 验证（§10.4）

```text
gofmt -l .                                  # 无输出
go test ./...                               # 默认构建全绿
go vet ./...                                # 无诊断
go test -race ./pool/ ./rpc/... ./aggregator/ ./internal/reference/ ./internal/runtime/
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild'
go list -deps . | grep ethclient            # 无输出
go test ./internal/transport/... ./pool/ ./gethcompat/... ./internal/transport/geth/...
```

Reference trace fixture：`internal/runtime/latest_reference_trace_test.go` 覆盖
§10.4 的五项要求（latest strict + 业务、finalized 候选+确认+业务、建立失败 fan-out
为零、成员变化基不变、单 ID 无嵌套）。

## 3. 验收时需要裁决的遗留项

1. **proposal 状态**：提案头部的「待 GPT 复核」状态与 16 项冻结决定的最终确认；
   任何单项推翻在 `next-phase-decisions.md` §3 追加修订。
2. **错误文本前缀**：R2 后建立失败的错误保留单层 `anchor:` 前缀（与旧链一致）；
   嵌套 Aggregate 的旧双前缀已消除。
3. **geth 兼容通道的残余 quirk**（已记录于 `transport-contract.md` §3）：位置匹配
   的 id 不校验、WSS 帧尾随垃圾不可复检。
4. **anchor-transport 验证测试改走 gethcompat**：这些测试比较的是兼容通道的
   HTTP/WSS 行为，属于过渡期消费者；native WSS 落地后应重写。
5. **`WithHTTPAdapter`**：按 D4 保留一个迁移周期（geth 选择报构造错误并指向
   gethcompat）；删除时机随 geth 通道退役决策。
6. **`eth_getProof`、Stream/WSS、proof 语义**：维持提案 §7/§8 的搁置决定。
7. **未迁移的缺口方法**（提案 §6.1 的 27 个 ethclient 缺口）：按 D3 以安全契约
   评审集合推进，数量只作结果；本分支未新增注册方法。
