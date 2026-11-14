# 文档导航与维护边界

这里保留现行合同、持续有效的未来设计和独立证据说明，不按历史工作阶段组织。已完成且被取代的施工计划／原型／review 日志由 Git 保留；待裁决问题和临时比较稿可留在本地 `docs/tmp/`，不纳入版本保存。未来设计必须明确实施状态，不因尚未实现就视为可清理材料。

## 从问题找到唯一负责人

| 要回答的问题 | 主文档 |
| --- | --- |
| 模块、依赖与执行 owner 属于谁？ | [codebase-topology.md](codebase-topology.md) |
| 对外承诺哪个完整 geth 业务集合？ | [go-ethereum-compatibility-policy.md](go-ethereum-compatibility-policy.md) |
| 当前哪些方法／传输／证据未闭合？ | [method-transport-coverage.md](method-transport-coverage.md)；下一步见 [project-roadmap.md](project-roadmap.md) |
| 前置生态兼容、官方语义检查、方法准入与比较的责任范围？ | [method-contracts.md](method-contracts.md)；生成装配索引 [method-rules.md](method-rules.md) 不手改 |
| Value、typed、附属字段与原始材料如何分通道交付？ | [return-value-design.md](return-value-design.md)；官方生成层 [wire-codegen.md](wire-codegen.md)（原始来源、显式标准勘误与通用 Jennifer） |
| reference／pending／EIP-1898 的方法差异？ | [moving-tag-matrix.md](moving-tag-matrix.md) |
| 裁决与 reference 门槛为何不合并？ | [acceptance-thresholds.md](acceptance-thresholds.md) |
| freshness 已接受什么、现状有什么缺口？ | [reference-freshness-policy.md](reference-freshness-policy.md) |
| resolver／ReadPlan／head seam 如何组装？ | [resolver-seam-contract.md](resolver-seam-contract.md) |
| unary transport／raw WSS／订阅的能力与生命周期？ | [transport-contract.md](transport-contract.md)、[wss-wire-contract.md](wss-wire-contract.md)、[subscription-contract.md](subscription-contract.md)，各自不重复定义其他层 |
| 验证阶段、场景、字段守恒与测试所有权？ | [method-verification-pipeline.md](method-verification-pipeline.md) |
| 本地合成、Anvil 链状态和外部 RPC 证据如何分层？ | [external-rpc-test-framework.md](external-rpc-test-framework.md)：未来测试设计，实施状态与真实来源边界分开声明 |
| capture／golden／回放能证明什么？ | [capture-golden-contract.md](capture-golden-contract.md)；有限真实 WSS 观察见 [wss-provider-evidence.md](wss-provider-evidence.md) |
| 外部 canary 的 provenance、状态与重跑门？ | [live-canary-contract.md](live-canary-contract.md) |
| 工具和发布入口、版本、产物与失败处置？ | [tooling.md](tooling.md)、[release-contract.md](release-contract.md) |

## 已确认的实施设计

- [Transaction 方法级 key 兼容修正](transaction-semantic-matching.md)：首个独立 transaction 检查点已分批提交；外层 key／内层完整值状态的双 switch 收敛已实施并有幂等与合并合同测试钉住。官方校验、拒绝及健康调度接线复用已有结构，不做内部局部修正；已执行本地公开路径与 Anvil 验证，尚未进行新公网 provider 采集。

## 临时评审记录

临时问题清单、比较稿和第三方评审原文只作本地讨论材料，不进入仓库导航或形成第二份实施合同。已采纳的结论并入上表对应的永久文档；未采纳建议不构成实施授权。

## 保留与删除原则

1. 规范字段与版本来自官方固定数据，不能由 provider 返回频率决定；项目额外约束必须明确标为保护／接受政策。
2. 当前行为、待实施方向、日期化观察分开写。正文不保留“上方说已完成、下方仍按旧阶段未实现”的镜像；接受／取代理由归 [decisions.md](../decisions.md)。
3. 有独立证明价值的原始样本、manifest／report schema、生成索引继续保留；不能因为阶段完成就删除可复现来源。
4. 多文档重复的问题合并到职责所有者；已完成施工单只在迁入仍适用规则后删除，不留空 redirect／兼容别名文档。
   持续有效的测试分层／未来框架设计不是已完成施工单；清理重复材料不得移除其独立验证目标、来源真实性要求或证据边界。
5. `docs/tmp` 是不纳入版本保存的本地待评审队列，不是第二个 roadmap。问题解决后将接受的合同合入主文档；未决不等于授权改实现。
