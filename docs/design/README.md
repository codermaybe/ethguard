# 文档导航与维护规则

状态：2026-11-17。完整文档系列已保存独立基线，首轮实现优化的审计修复及本轮本地验证已完成；性能裁决和剩余进度由路线图维护。本系列按当前知识归属组织，已合并重叠的流程、交易、门槛、捕获与工具说明。后续文档整理继续遵循先明确合同、再实施代码的顺序，事实更新不自动改变生产合同或代表整体验收通过。

## 1. 从问题找到唯一负责人

| 问题 | 主文档 |
| --- | --- |
| 当前能力、剩余阶段和执行顺序 | [项目路线图](project-roadmap.md) |
| 模块、依赖、ReadPlan、resolver与canonical读取流程 | [代码库拓扑与执行合同](codebase-topology.md) |
| 方法保证、业务门槛、有限兼容与独立交易规则 | [方法合同](method-contracts.md) |
| 受保护Value、规范编码、typed与材料通道 | [返回值合同](return-value-design.md) |
| reference候选／确认、freshness与F1未决项 | [Reference与freshness](reference-freshness-policy.md) |
| 固定官方来源、勘误、生成策略和新鲜度 | [类型生成合同](wire-codegen.md)；原始数据操作见 [spec说明](../../internal/spec/README.md) |
| 逐方法tag／EIP-1898／pending | [引用矩阵](moving-tag-matrix.md)；其结构由测试检查 |
| 实际装配的只读投影 | [生成方法索引](method-rules.md)，禁止手改 |
| 公开类型与业务兼容目标 | [兼容政策](go-ethereum-compatibility-policy.md) |
| 已有入口与逐方法／传输证据缺口 | [覆盖清单](method-transport-coverage.md) |
| HTTP协议与adapter生命周期 | [Transport合同](transport-contract.md) |
| raw WSS握手／帧／通知与连接 | [WSS wire合同](wss-wire-contract.md) |
| 订阅对象、snapshot、会话、Pull、终止与S1 | [订阅合同](subscription-contract.md) |
| 方法验证、场景、capture／golden、Anvil、工具与材料保留 | [验证与证据合同](method-verification-pipeline.md) |
| 固定canary plan、manifest/report/recheck及观察状态 | [Live canary合同](live-canary-contract.md) |
| 正式发布门禁、固定工具、产物与失败处置 | [发布合同](release-contract.md) |

## 2. 当前合同、目标与历史

当前合同说明现行保证与实现差异；目标设计必须标明未实施部分；待裁决问题写回对应正式所有者，不依赖未入Git的临时清单。文档允许完整重写，不要求保留旧段落或逐句拼接；每条有效规则迁入后再删除被取代文件，并修复导航、代码注释与实际引用。

历史decision的ID、日期和原判断保留在 [决策档案](../decisions.md)。当前类型、注册与生命周期不由旧Status推导；阅读当前合同即可理解执行约束，查决策仅用于理由追溯。整理不能静默改变保证或在没有实现证据时把目标写成事实。

## 3. 证据与生成材料

[WSS provider观察](wss-provider-evidence.md)、canary／transport／release中的dated evidence和docs/samples保存当时事实。摘要、原始输入、错误、来源身份和日期不能改写成新观察；合成、Anvil和provider各自记账。真实证据不能因无测试引用而删除或用合成替换。

method-rules.md、ethapi生成代码／audit、固定schema、JSON report/schema和golden保留各自生成或验证流程。文档整理不手改这些资产。包README只介绍入口和职责，具体规则集中在本系列，避免复制第二份合同。

## 4. 本次整理的归属变化

| 已合并内容 | 当前负责人 |
| --- | --- |
| resolver seam与读取阶段 | codebase-topology.md |
| 业务Strict门槛／独立交易兼容与对象规则 | method-contracts.md |
| finalized门槛／确认与F1 | reference-freshness-policy.md |
| capture/golden／外部测试设计／工具与证据保留 | method-verification-pipeline.md |
| 原architecture导航 | 本导航及codebase-topology.md |

旧文件正文可从Git历史追溯，不保留空重定向文件。2026-11-17版本只完成文档组织、现行事实与未决项整理；手写定义替换和逐模块实现优化留到下一代码阶段。

## 5. 临时材料存放与Git边界

临时材料唯一位置为仓库根下的 `docs/tmp/`，由 .gitignore 排除；原 docs/design/tmp 已归并并退出使用。阶段执行表、调查草稿、审计原文、进度与总结、原始运行日志等非关键材料不纳入 Git，不通过 force-add 绕过规则。临时辅助程序与构建／profile产物优先放系统临时目录，避免被普通Go包扫描或发布。

稳定的业务／模块合同、已采纳决策、当前路线图、公开使用说明及必要的独立回归资产继续按既有owner版本化。临时执行计划引用这些合同，不成为第二份规范；执行过程不设等待主控回应的中间门槛；整体审计后只把长期有效的结论与实际状态回写正式文档。已有真实证据、测试fixture和来源审计不因“阶段性”而删除或移入忽略目录。
