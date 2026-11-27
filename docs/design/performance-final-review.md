# 性能优化终审交接

**本地主控终审通过，无阻塞项；性能批次尚待用户转交 Gemini 独立外审。** 本轮代码范围为 `0550b7a982adbf6ef42c5fc541f8875e0d0378ef..e40851d`，按 Wave 0 → 1A → 1B → 2A → 2B → 3 执行。均为本地提交，未推送。

主控负责设计、编排、交叉审查、独立验收及提交；两个执行者负责生产改动。详细过程与测量口径见 [分阶段验证](performance-verification.md)，生成合同见 [wire-codegen](wire-codegen.md)。前一轮 ethapi 标准管线的 Gemini PASS 不代表这一轮性能修改已经通过外审。

## 实际改动

| 阶段 | 本地提交 | 最终实现 |
| --- | --- | --- |
| Wave 0／1A | `23e24db` | 修复同样本基准；严格标量使用 ASCII 原语；仅精确 pattern 绑定；Quantity 删除 big.Int 往返 |
| Wave 1B | `31feb32` | 共享 JSON 字符串成功快路径；标量编码复用已有紧凑 JSON，继续消费生成方法根 |
| Wave 2A | `304f4ce` | Log／Receipt 复用同一份已检查原文；Bloom 增量计算；两个成功 Receipt 读取入口使用 admitted 交付 |
| Wave 2B | `d6fa11e` | 唯一试点 `ValidateCreateAccessListResult`；同源生成完整值约束，替代末尾整对象 Marshal→New 往返 |
| Wave 3 | `e40851d` | Block 复用 Header 产物；Transaction 复用已验证上下文；投影路径单次分段；请求参数编码一次后组装外层 |

## 累计测量与正确性验收

以下是最初基线到最终源码的同样本实测中位数，未将各阶段百分比相乘。Linux amd64、Go 1.26.11、GOMAXPROCS=2；每版四份样本，完整区间及分配字节见分阶段验证文档。

| 固定 fixture | 耗时旧 → 新 | 耗时变化 | 分配次数旧 → 新 |
| --- | --- | --- | --- |
| Quantity 编码，典型值 | 1.323µs → 0.226µs | −82.9% | 13 → 3 |
| Log 整数组，64 条 | 6.392ms → 3.435ms | −46.3% | 约 36,427 → 20,754 |
| Receipt，1000 条 Log | 116.806ms → 78.698ms | −32.6% | 约 644,605 → 395,534 |
| AccessList Encode，64 项 | 1.193ms → 0.526ms | −55.9% | 4,111 → 1,699 |
| 完整 Block 准入，100 笔交易 | 52.917ms → 43.661ms | −17.5% | 约 322,950 → 258,174 |
| FeeHistory Encode，64 块 | 0.903ms → 0.667ms | −26.2% | 约 7,245 → 6,208 |

三源 AccessList HTTP 的分配字节约降 30%，但耗时区间重叠，不能将中位数 −25% 当成稳定网络收益；Transaction HTTP 耗时基本持平。Wave 3 请求拼装的 64B／512B／128KiB 参数样本分别约快 18%／39%／49%，每次多 1 次分配。投影加长复测中 Block100Tx 约快 4%，每次少约 1,700 次分配。

最终投影修正后重新通过全仓普通测试和 race：各 55 个测试包，另 8 包无测试。vet、build、tidy、默认／anvil 两路 lint、65 包 anvil 编译均通过；独立生成 Go／audit 字节一致。标量、JSON、值验证、路径和请求拼装有冻结差分与 schema 变异见证，具体执行次数及日志来源见分阶段验证。七个终态场景另采 CPU／alloc profile，与上述计时分开。

## 需要维持的边界

1. **输入语义由准备层负责。** provider 扩展仍按既有规则投影，AccessList 不因官方根对象封闭而拒绝扩展。Log 精确 key 准入和大小写别名编码不能机械合并；timestamp 错误仍归编码阶段。
2. **模型值由同源生成约束验证。** 对可编码的 `v`，新方法根验证器的接受结果等价于 `NewCreateAccessListResult(v.MarshalJSON())`；只读、不重编码、不规范化输入。nil 指针、指向 nil／空 slice、非法 UTF-8 的编码语义、直接伪造标量及方法根附加约束均有反例。相等的是接受结果，未承诺错误对象与 JSON 构造器完全相同。
3. **预算与业务证明各自保留。** 只有同一份未改写字节可以复用安全检查；Receipt admitted 交付仍检查最终规范字节预算。它只用于两个已完成对象准入的成功读取路径，原始／批量／订阅入口继续自保。值验证不代替密码学承诺或请求绑定。
4. **接口保持窄。** 只新增一个公开 ethapi 方法根验证入口，其余辅助私有；选择进入既有 Manifest。可达 schema 出现尚不支持的形态时生成失败，没有运行时 schema 解释器，也没有为所有模型铺开 Validate。

## 审计后保留的实现

- Block／Logs 不迁入 ProcessCanonical：ByNumber 的 PendingObserve 禁止该组合；ByHash fullTx=false 的准入与编码拒绝集合不同；Logs timestamp 也存在阶段差异。阶段见证已进入测试。
- Transaction 仍保留 wiremask any 树和原始 exact-key 视图，分别承担开放扩展折合、投影、安全预算及原始声明检查。本轮只删除可证明冗余的绑定解析。
- 异构 field aggregation 保持原算法，未引入额外候选索引或重放状态；Log／FeeHistory 不扩大值验证试点。
- 投影使用局部 8 段数组，超长路径正常分配；无 Trie。rpcwire 只减少单次请求内的重复扫描，尚未跨来源复用冻结参数数组。

这些是本轮范围判定，不是已实现能力。未来扩展需重新证明对应语义边界。

## 已知成本与验收限制

- 长非法 ASCII JSON 在快路径探测后仍进入标准库错误路径；64KiB 拒绝样本约慢 16%–25%，接受域与错误语义保持。
- 新值验证器必须重新检查可直接伪造的标量。已测成功值验证为零分配，完整 AccessList Encode 和 typed 交付仍有各自成本。
- Bloom 去掉随日志增长的中间树，但整个函数固定 5 次分配；空日志旧实现为 3 次，不能称其零堆分配。
- rpcwire 请求拼装每次增加 1 次分配。接受的是参数重复扫描消失带来的已测收益，不隐藏小对象缓冲成本。
- 投影首版出现可重复的 CPU 回退，已在终审中拦下；最终采用单次分段，加长复测不再复现该回退。
- 测量为本机固定 fixture、三源本地 HTTP。网络调度波动与真实 provider 延迟不同；本轮没有执行真实 provider replay、Anvil 链或正式发布。

## Gemini 建议复审入口

| 复审问题 | 主要实现与反例 |
| --- | --- |
| 快路径是否守住标量接受域、转义及错误回退？ | [strict.go](../../internal/ethcodec/strict.go)、[JSONString](../../internal/ethcodec/jsonstring.go)、[标量差分](../../ethapi/strict_scalar_contract_test.go)、[JSON 差分](../../ethapi/jsonstring_contract_test.go) |
| 是否完整同源生成方法根，升级时同步收紧？ | [值验证生成](../../internal/wiregen/value_validation.go)、[共用约束](../../internal/wiregen/assertions.go)、[schema 变异](../../internal/wiregen/value_validation_test.go)、[值态差分](../../ethapi/value_validation_test.go)、[真实消费者](../../internal/ethobject/accesslist/encode.go) |
| 同一字节与最终预算的证明是否充分？ | [Log checked 入口](../../internal/ethobject/log/log.go)、[别名冻结合同](../../internal/ethobject/log/alias_semantics_test.go)、[Receipt 安全反例](../../internal/ethobject/receipt/json_checked_test.go)、[admitted 交付](../../internal/typed/receipt_admitted_test.go)、[成功读取调用点](../../transaction_reads.go) |
| Block／Transaction 是否保持承诺、别名和错误阶段？ | [Header 复用见证](../../internal/ethobject/block/header_reuse_test.go)、[交易绑定见证](../../internal/ethobject/transaction/context_binding_test.go)、[路线阶段见证](../../internal/methods/canonical_route_stage_test.go) |
| 请求拼装和局部路径匹配是否等价？ | [请求冻结 oracle](../../internal/rpcwire/request_assembly_test.go)、[路径冻结 oracle](../../internal/projection/path_matcher_test.go) |

## 复现口径

从基线和最终代码提交分别创建只读测量副本，使用同一 Go 1.26.11 工具链、fixture 与基准源码。原始基线缺少或带缺陷的 Log 整数组、Block 合法大样本及 Receipt 基准，仅复制最终对应测试文件；不得复制新生产实现。先验证 fixture 能通过实际入口，再分别编译测试二进制。

停止本任务的测试／编译负载后，设 `GOMAXPROCS=2`，按旧→新→新→旧串行运行，每轮 `-test.count=2 -test.benchmem -test.cpu=2`。常规微基准 250ms，三源 HTTP 固定 100 次；投影修正专项使用 1s。CPU／alloc profile 独立采集，不混入计时结果。汇总中位数、完整样本区间和分配，阶段百分比不相乘。

持久恢复入口是本文件列出的提交及仓内测试。临时完整材料位于 `/tmp/ethguard-wave01-review/` 和 `/tmp/ethguard-continuation-review/`，含源码摘要、冻结二进制、原始日志、汇总及 profile；这些临时路径不是代码构建的依赖。rpcwire 百万次 fuzz 记录为执行者原始工具会话摘录，其余主控日志与该摘录明确区分。
