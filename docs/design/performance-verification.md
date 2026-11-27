# 性能优化计划与首次验证

本轮范围：Wave 0 → Wave 1A。主控负责设计、编排、只读审查与独立验收；两个执行者分别拥有 codec 与生成器。后续 Wave 保持独立，不因本轮通过而标记完成。

## 基线与阶段

首次源码基线为 `0550b7a982adbf6ef42c5fc541f8875e0d0378ef`；相对交接中的 `ce1c592` 只有 `ethapi/README.md` 修改。测量使用相同 Go 1.26.11 工具链、fixture、测试代码和 CPU 设置，旧新源码分别编译。所有运行时规则仍由现有标准生成、对象和执行合同定义。

| 阶段 | 范围 | 退出条件 |
| --- | --- | --- |
| Wave 0 | 审计基准等价性；覆盖标量、对象、交付、聚合、投影和三源 HTTP；采 CPU/alloc profile | 明确输入有效性、计时范围、真实执行入口及未覆盖范围 |
| Wave 1A | 严格标量 ASCII、native 精确 pattern 绑定、Quantity 去数值往返 | 接受域、规范输出、错误及附加 schema 约束保持；消费者收益实测 |
| Wave 1B | 标量 JSON 成功快路径与兼容链重复编解码 | JSON 转义、非法输入、尾随内容及拒绝成本独立验证 |
| Wave 2A | 安全检查结果复用、Receipt 已准入交付；Bloom 独立切片 | 所有权、安全预算与错误阶段保持 |
| Wave 2B | AccessList 方法根值验证试点 | 同源规则、完整约束、无重解析；收益足以支持复杂度 |
| Wave 3 | Block/Transaction 局部优化及条件性路线扩围 | 内容承诺、pending、证据、裁定与交付等价 |

## Wave 1A 实施合同

- `ethcodec` 提供严格、无兼容的字符串谓词。Quantity 无人为限宽；Quantity64/256 分别限 16/64 hex digits；DATA 必须为完整字节；Address 只允许小写 `0x` 前缀但数字部分可混合大小写；HexByte 允许 1–2 个小写 hex digits；StorageKey 允许 0–64 个小写 hex digits，含奇数长度；DecimalString 保留零且拒绝前导零。
- wiregen 仅对完全匹配的 native pattern 绑定严格原语。未知和近似 pattern 保持 regexp；普通 schema 附加 pattern、const、enum、uniqueItems 和组合的字符串身份合同不改变。
- 保留现有 Hash/Bloom/BlockNonce 固定宽 ASCII 能力，不把历史收益计入本轮。
- `CanonicalQuantity` 保留 JSON 解码、具名大小写兼容、严格拼写与错误文本，合法 hex 直接返回；`EncodeQuantity` 复用这一结果。仍需十进制的 Normalize/QuantityDecimal 合同不变，peer-count 的特殊前导零兼容不传播。
- JSON 解码快路径、模型 Validate、对象解析、admission/processor 装配和公开 typed 交付不在本轮生产修改范围。
- Go 与 audit 仅通过生成器更新；固定来源、Overlay、Manifest 选择与表示政策均保持。

## 测量与正确性验收

差分参考必须冻结原始 regexp、JSON 解码、大小写兼容和旧 Quantity 转换，不能调用已优化函数充当旧实现。验证接受/拒绝、成功值、错误类型与文本；针对边界长度、大小写、转义、尾随内容、空/null、非法 UTF-8 及长输入有确定性反例。随机差分记录实际次数及种子，不能用生成器自述代替执行结果。

同样本基准分别记录 ns/op、B/op、allocs/op。先编译，停止其它测试/编译负载后按旧→新→新→旧串行测量；网络基准使用本地三源 HTTP，计时前校验合法输出与支持来源，固定请求和准备成本。CPU/alloc profile 只用于归因，与未启用 profile 的时间数据分开。

`BenchmarkAccessListRoute` 内的历史 `baseline-encode` 来自更早的实现，本轮只使用 `production-encode`，跨源码快照比较。冻结参数基准须先修复额外数组嵌套，确认同一请求及同等 ID/method 检查；仍调用 `json.Marshal` 的原型应明确称为 prepared-array remarshal，不宣称直接拼接。

投影用 Block100Tx fixture 只证明 schema 形态，不能代替完整 block admission 的签名、交易根和 header 绑定基准。完整内容检查需使用能通过该入口的 fixture，并明确合成材料与真实链证据的区别。

验收包含受影响包测试、生成新鲜度、全仓普通测试、相关 race、静态检查及实际注册消费者。耗时受调度噪声影响时分别报告确定的分配变化与不确定的延迟变化。零分配、数量级改善、错误顺序完全一致均为待证明命题。

## Wave 2B 的已审定设计，尚未实现

仅试点 `ValidateCreateAccessListResult(v AccessListResult) error`。对可成功编码的 v，其接受结果应等价于 `NewCreateAccessListResult(v.MarshalJSON())`；生产实现只读遍历模型，不执行这一 JSON 往返，不折合值或补字段。

这里比较的是编码后的 JSON 语义。例如直接构造的自由文本包含非法 UTF-8 时，现有 Marshal 会替换为 U+FFFD；值验证不能擅自增加 UTF-8 拒绝政策，也不能修改输入。若将来文本带 pattern、enum 或唯一性约束，生成能力必须证明其编码语义等价，不能仅按原始 Go string 字节猜测。

准备层保留 provider 别名/顺序、扩展投影、null 折合与方法必填政策；`New*` 与 `Validate*` 从相同 IR 执行完整方法根约束。官方封闭对象并不意味着运行时拒绝已获准投影的 provider 扩展。模型值验证不证明原始输入存在性、JSON 安全预算或业务准入。

保留 AccessList 的逐字段检查和最终一次 Marshal，仅删除末尾整对象 New* 重解析。外层 nil 指针、指向 nil slice 和指向空 slice 依编码语义区分。生成选择进入既有 Manifest，不能在编译器硬编码方法；可达约束必须完整支持，否则生成失败。只公开一个方法根入口，其余辅助函数私有。新增约束的独立变异反例同时驱动 New*、Validate* 及实际消费者。

试点收益应在 Wave 1A/1B/2A 之后重新测量。不得把当前已有编码分配宣称为试点能删除的成本；没有实测依据时不承诺 Validate 零堆分配。

## 尚未解除的条件

Log 精确 key 准入与大小写别名编码的双语义继续保留；blockTimestamp 错误仍归编码阶段。BlockByNumber 的 PendingObserve 与现有 CanonicalProcessor 装配互斥；BlockByHash 单独评审。生成模型、JSON 安全、对象完整性、请求绑定与多源裁定分别拥有自己的证明，不合成一个含糊的 validated 标签。

## 本轮结果

Wave 0 → Wave 1A 已通过本地验收。最后补审发现的历史 Receipt 大样本基准也已单独修复和补测，不修改生产规则。

本轮由 `performance_baseline` 执行 codec 与基准修正，`validator_foundation` 执行 wiregen 与生成资产；两者完成后交叉只读审查，主控负责独立验收和本文。所有生产改动限于 Wave 1A，尚未提交或推送。

Wave 0 新增两项持久基准：`BenchmarkLogResultEncode` 测量真正的整数组 Encode；`BenchmarkBlockObject` 的 1／100 笔交易样本包含真实签名、不同 nonce、正确交易根及 header hash，经 AdmitBlock 和 Transactions 验证，分别测 Encode 与完整准入。原 `BenchmarkLogArrayEncode` 是逐项 EncodeOne，不能混用名称。冻结参数对比先核对完整请求字节及 ID／method 错误，参数从同一数组拆为元素；两种方案仍执行 Marshal，没有实现直拼。

测量脚本与原始材料位于 `/tmp/ethguard-wave01-review/`。旧版本由上述 commit 的 archive 构成，只复制相同的测试及基准文件；所有生产文件保持旧版本。旧新各自编译测试二进制，JSON 标量与 Quantity 的冻结参考、block fixture、冻结参数等价性见证均在两版通过。每个微基准 200ms × 2，三源 Aggregate 固定 200 次 × 2，公开交易查询固定 100 次 × 2；全部按旧→新→新→旧串行运行，每版共四个样本。这里只报告本地固定 fixture 的中位数与分配，不作为生产延迟保证。

测量环境为 Linux amd64、Go1.26.11、Intel i5-8300H、GOMAXPROCS=2。最终核对：基线除 8 份同样本测试材料外，1,359 份文件与冻结 commit 一致；749 份最终 Go／来源／生成／依赖输入的哈希保持。原始数据和 profile 是临时证据，持久恢复入口是上述 commit、本仓基准及本文；本轮没有执行真实 provider replay、Anvil 链或正式发布。

独立正确性验收：

- `go test ./... -count=1` 与 `go test -race ./... -count=1` 各 55 个测试包通过，另 8 包无测试。
- `FuzzStrictASCIIMatchesFrozenPatterns`、`FuzzQuantityMatchesFrozenRoundTrip`、`FuzzStrictScalarConstructorsMatchFrozenRegex` 各执行 1,000,000 次通过，分别使用 64KiB／8KiB／64KiB 输入上限和 2 个 worker。确定性用例另覆盖 1MiB 严格原语输入；百万次数不是对所有长度的穷尽证明。
- `go vet ./...`、`go build ./...`、`go mod tidy -diff`、默认与 anvil 两路 lint2.15.0 均通过，两路 lint 各 0 issues。`go test -tags anvil -run '^$' ./...` 完成 65 包编译，未启动链。
- 独立输出 Go／audit 与工作区逐字节一致；官方来源、Overlay、Manifest、依赖及公开 ethapi 声明未变。格式和 diff 检查通过。
- 初次沙箱全仓检查因禁止本地 socket 失败，放行本地 httptest 监听后完整重跑通过；该环境失败不计为代码回归。lint 缓存目录只读产生告警，但检查完整结束并返回成功。

全仓验收后只有 Receipt 基准准备代码变化：保留捕获原文的 from／to 等字段，只更新合成 logs 与匹配 bloom；原 geth receipt Marshal 会丢失必填 from，旧基线因此也失败。修正已原样用于旧新两版，每组计时前检查 Encode 成功，0／100／1000 条日志全部通过；增量 methods lint 为 0 issues。生产文件、其它测试与生成资产未变，没有将此前全仓结果冒充为随后新增基准的执行证据。

首轮完成 132 组旧新对照，Receipt 补测另增 3 组，共 135 组。以下为本地中位数，分配列为每次操作次数；约数表示采样和池复用带来的微小波动。

| 路径 | 耗时旧 → 新 | 耗时变化 | 分配次数旧 → 新 | 分配字节变化 |
| --- | --- | --- | --- | --- |
| CanonicalQuantity，典型值 | 1.154µs → 0.292µs | −74.7% | 11 → 3 | −45.0% |
| EncodeQuantity，典型值 | 1.329µs → 0.458µs | −65.5% | 13 → 5 | −40.9% |
| Log 整数组，64 条 | 6.407ms → 5.358ms | −16.4% | 36,427 → 34,388 | −1.8% |
| Receipt Encode，100 条 Log | 12.362ms → 10.353ms | −16.3% | 约 64,848 → 62,054 | −1.6% |
| Receipt Encode，1000 条 Log | 131.107ms → 111.222ms | −15.2% | 644,606 → 616,590 | −1.1% |
| FeeHistory Encode，64 块 | 0.901ms → 0.792ms | −12.0% | 约 7,245 → 7,244 | 基本持平 |
| AccessList Encode，64 项 | 1.209ms → 1.152ms | −4.8% | 约 4,111 → 4,110 | −0.4% |
| typed Receipt，1 条 Log | 0.227ms → 0.208ms | −8.4% | 1,197 → 1,172 | −0.6% |
| 完整 Block 准入，100 笔交易 | 52.603ms → 51.220ms | −2.6% | 约 322,946 → 322,931 | +0.1% |
| 三源 HTTP AccessList，64 项 | 6.571ms → 6.289ms | −4.3% | 约 25,825 → 25,823 | −0.1% |
| 三源 HTTP Transaction Aggregate | 3.340ms → 3.331ms | −0.3% | 8,921 → 8,921 | 基本持平 |

八类生成标量 Plain 样本耗时下降约 30%–66%，分配不变；纯严格谓词成功调用零分配，构造器仍进行 JSON 解码，不能称构造器零分配。CanonicalQuantity 的 4KiB 合法输入从约 255.5µs／93 次分配降至 27.8µs／3 次；非法末尾仍保留完整错误构造成本。

Transaction 方法 Normalize／Process 分配不变，耗时约 −2.7%／−1.6%，旧新样本区间重叠。三源典型 AccessList、Receipt Aggregate 和公开 TransactionByHash 均基本持平；AccessList 64 项 HTTP 样本旧新区间为 6.01–7.01ms／5.80–6.80ms，不能将中位数下降解释为稳定网络收益。泛型 field aggregation 分配不变。投影两个扩展样本首轮耗时 +8.8%／+11.1%，随后按旧→新→新→旧、1s × 3 加长复测，各版六份样本得到 Block100Tx −0.4%、Receipt16Logs −0.7%，未复现稳定回归，保留初测和复测全部记录。

冻结参数的准备数组原型在 128KiB 样本中约为元素重编码成本的一半，并由 13 次分配降至 11 次；这是原型间的基线对比，既非 Wave 1A 的生产收益，也不能证明“直拼”方案已完成或值得优先接入。

## 剖析结论与下一步验收口径

8 个代表场景各采旧新 CPU、alloc_space、alloc_objects。剖析与计时分开，包含进程准备／清理成本；下列分配占比均指 alloc_space（分配字节）。累计占比只用于定位调用链，互相可能重叠，不能相加或直接当作可删除成本。

- **共享 JSON 工作仍是第一优先。** 新版 64 条 Log 的采样分配中，Unmarshal 累计约 50%、jsonsafe.walk 约 35%；100 笔交易块中分别约 48%／28%。Wave 1B 验证字符串成功快路径，Wave 2A 再验证同一未改写字节的安全门复用。两者都不能省掉原始输入语义、预算或错误阶段。
- **AccessList 单对象试点有证据支持。** 64 项三源 HTTP 的采样分配中，accesslist.Encode 累计约 53%、末尾 NewCreateAccessListResult 约 25%，而 field aggregation 约 17%。这说明生成对象重解析是实际成本，但 25% 不是 Validate 试点的承诺收益；标量重检、最终编码与其它阶段仍存在。
- **Block／Transaction 仍需各自优化。** block 样本 map 分配约占 22%，RawMessage 复制约 12%；Transaction Process 中 map 分配约 26%，jsonsafe.walk 累计约 26%。这些并非标量 ASCII 能消除，应按 Wave 3 审计准备产物与重复反序列化。
- **投影 Trie 保持次级局部候选。** Block100Tx 扩展投影样本中 strings.genSplit 约占 6% 分配，而 CoreObjectFingerprint 累计约 90%。先用单独基准约束 Trie 的收益上限；不能仅因路径拆分明显就宣称它是主瓶颈。异构 field aggregation 仍由候选收集、JSON 安全和树转换占主导。

下一步仍为 Wave 1B → 2A → 2B → 3。Wave 1B 单独比较无转义合法值、含转义合法值、非法 UTF-8、裸控制字符、尾随 JSON 及长非法输入；保留标准库错误路径与接受域。Wave 2A 先写明“谁证明了哪份字节、预算是否变化、错误属于哪一阶段”，再删除相应重复检查。Wave 2B 只开放 AccessList 方法根试点，验收同源约束、只读不改值、当前模型到编码语义的等价性，并在前两阶段新基线上重新判断收益。未实施这些阶段，也没有改变 PendingObserve／CanonicalProcessor 的装配约束。
