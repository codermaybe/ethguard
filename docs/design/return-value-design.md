# 返回值设计：受保护结果域与规范编码

状态：阶段 0 已在 8aac099 验收。阶段 1 已在 48e2c64 验收，0041 只接入数量和成功 DATA：`rpc/result.go` 与 `rpc/internal/wire/encode.go`。0042 只接入 `eth_feeHistory`：比较键仍是方法现有的 feeHistory 键，不是整段规范 JSON；Value 是受保护字段的规范对象，编码器在 `rpc/internal/objects/feehistory/encode.go`。收据、交易、日志、access list、eth_call 信封和区块重编码仍不实现。执行顺序、任务出口和核对清单只写在 `project-roadmap.md` 的「返回值框架」；本文不另写一套任务。

2026-09-26 起写明的执行顺序、分母、派生字段和获胜簇规则，以及 2026-09-27 锁定的第 14 节，都是这份依据，不再标成不定档。第 13 节仍只是外部意见对照。阶段 3a、3b 的方向已锁，阶段 0 不实现，也不据此给每个字段另写标准。

仍开放、且不挡住阶段 0 的只有两项。`from` 在 CPU 测量之后要不要定义成派生字段，默认留在域外；没有测量不能进域，也不能为此把整票判掉。Provenance 的字节上限没有数字，不在这里补。

未迁移对象的 Value 仍是某一份原始载荷（4e 删减除外）。`from` 和 `size` 还会出现在这些 Corroborated 结果里。数量和成功 DATA 的 Value 是规范编码。`eth_feeHistory` 的 Value 是受保护字段的规范对象，键外字段不进 Value。这不表示区块已经语义安全：那些字段要到阶段 3b 才从区块 Value 去掉。

`return-value-normalization.md` 里的问题记录、事实核对和执行标准已并入本文。模型意见不再单独成文，避免和正文分成两套设计。

关联：`architecture-record.md`、`method-verification-pipeline.md`、决策 0020、0024、0025、0028、0029、0039、0040。

---

## 1. 结论

下面第 1 点到第 8 点是规范编码全部落地后的结论。阶段 0 已完成，没有把全部方法改成规范编码。阶段 1 只让数量和成功 DATA 的 Value 变成规范编码。0042 再让 `eth_feeHistory` 的 Value 变成受保护字段的规范对象，比较键仍是 feeHistory 键。其余未迁移方法的 Value 仍是代表源原文。阶段 0 做的是：代表源改成捕获顺序第一个；Value、Evidence、Withheld 与这个来源同源；StrictHeadAnchor 发出的区块 hash 改成小写。见第 7 节和第 11 节。

1. `Decision.Value` 对已注册方法只表示受保护结果，不再同时充当来源原文。
2. 受保护结果按方法定义为受保护结果域。Value 是该域的确定性规范编码。
3. 编码器从已准入载荷生成规范字节，不从比较键反序列化，也不把多个来源的字段拼成一个新对象。
4. 执行顺序是：逐源 EncodeCheck，再按方法自己的比较键比对，Resolve 只跑一次，最后 EncodeCommit。编码失败不触发第二次投票，本轮分母不缩小。
5. 获胜簇内每个支持源的规范字节必须相同。不相同是比较键太窄或编码器越界，是代码缺陷，不是来源分歧。
6. 逐字节相等是必要条件，不是充分条件。编码器不能靠删掉应受保护的字段来制造相等。字段表、变异测试和往返测试负责补上这一层。
7. 来源证据走独立通道。已迁移的数量、成功 DATA 和 `eth_feeHistory`：代表源只决定审计锚点，不决定 Value。未迁移方法的 Value 仍是代表源原文（4e 删减除外），见第 7 节。原文不写入 Provenance。
8. 第 14 节是已锁定口径。它不给每个字段另写标准。code 与 message 留在代表源上；区块 Value 只收已经核对过承诺的内容，`from` 默认留在域外。为什么不改成胜者原文透传，也不再加一层最终响应投影，见第 13 节。仍开放、且不挡住阶段 0 的，只有 `from` 的测量后归属和 Provenance 的字节上限。

## 2. 事实前提

这些事实约束本文，避免再把问题写成“响应谁先到”。

生产路径按本次调用捕获的成员下标排列观察，不按响应到达顺序。`pool.Collect` 把结果写回 `emissions[i]`，aggregator 按同一下标构造观察。配置顺序就是这个顺序。把其中一个响应延迟，选中的原文字节不变。

当前真正不稳定的是规则不统一，而且没有写在一个地方。下表是阶段 0 代码落地之前的现状。代码没改之前，这张表仍然属实：

| 路径 | 阶段 0 之前 |
|---|---|
| Strict 的 `cluster` | 该比较键下后写入 map 的那一份，也就是捕获顺序中的最后一个 |
| CallRevert 的 `representative` | 簇内切片的最后一个 |
| Median | 与中位数值相等的样本中的第一个，注释里写了这条规则 |
| Local | 第一个健康源；这是选取策略，不是平局规则 |

因此同一组 `"0xA"` 和 `"0xa"`，Median 可能返回 `"0xA"`，Strict 返回 `"0xa"`。直接构造观察切片时（测试、replay），换顺序也会换 Value。`cluster` 的注释说 Value 与到达顺序无关，生产路径只是因为调用方已经排好了序。

同一个 Decision 里，Value 和 Evidence 可以来自不同来源。Value 取簇内最后一个，Evidence 取第一个带 evidence 的观察。输入 `[a, b]` 时，Value 是 b 的字节，Evidence 是 a 的。这和“证据留在它所属的来源上、不跨源拼接”抵触。

会被收进同一簇、但仍然留在原文里的差异，比“所有前导零”窄：

- 多数数量路径不接受 QUANTITY 前导零。`NormalizeWireQuantity` 直接判无效。前导零目前只在少数路径被折成同一键，例如 `net_version` 的十进制、`net_peerCount`，以及未经准入的锚点区块号。
- 实际会进同一簇的有：hex 字母大小写、部分路径的 `0X` 前缀、JSON 键序、空白、JSON 字符串转义，以及比较键没有覆盖的字段。
- `gasUsedRatio` 今天解进 `[]float64` 再编码，所以 `0.5` 和 `0.50` 会进同一簇。这是数值相同，不是写错格式。

现有 golden 不钉 `Decision.Value` 的字节，所以“golden 偶发不一致”现在还不会暴露。字节不稳定仍然影响调用结果，已核对过的例子：

1. 两个来源返回同一 revert data，一个 `code` 为 3，一个为 -32000。配置顺序 `[3, -32000]` 时，ethclient 形状的 `RevertError` 拿到 -32000；顺序反过来就拿到 3。延迟任一方都不改变这个结果。
2. 三个来源都返回 `eth_call` 结果 `0xdeadbeef`，其中一个写成 `"\u0030xdeadbeef"`。比较键相同，Status 可以是 Corroborated。转义那一份如果成为代表源，geth 的 hexutil 不解 JSON 转义，typed 读取失败；它不在代表位置时，调用成功。
3. StrictHeadAnchor 把代表源 hash 的原始拼写放进随后发给所有来源的请求。第三源写成 `0X…AB` 时，后面的 `eth_getBalance` 都会带上这个拼写。FinalizedHeadAnchor 会先转小写，两条路径不一致。
4. 区块比较键只有 `number|hash`。`size` 这类键外字段仍留在获胜载荷上，于是 Corroborated 的 Value 里可以出现只有一个来源断言过的内容。

代码里已经有未声明的规范化和未声明的代表源选择。写路径返回小写 hash；finalized 参考把 hash 转小写；`decodedQuantity` 读规范形式；`eth_call` 的 4e 删字段后会把整个对象重新序列化。所以“返回来源原值”现在并不是一条处处成立的规则。

## 3. 术语

比较键、受保护结果域、规范编码、EncodeCheck、EncodeCommit、域外字段，都是规范编码落地后才启用的词。阶段 0 不实现它们。代表源从阶段 0 起就有，但两段含义不同，不能把后一段套到前一段上。

| 术语 | 含义 |
|---|---|
| 比较键 | 方法用来分簇的键。EncodeCheck 之后才使用。数量和 DATA 的键就是规范编码；区块的键仍是 `number\|hash`。整段规范 JSON 不代替比较键 |
| 受保护结果域 | 方法声明返回、并且能被佐证覆盖的字段。构成见第 4 节 |
| 规范编码 | 按第 5 节把受保护结果域写成确定的 JSON 字节 |
| EncodeCheck | 裁决前，逐个来源回答：这份载荷能否在不改变原值的前提下编成规范字节。失败记 Fault |
| EncodeCommit | 唯一一次 Resolve 之后，把获胜簇里已经相等的规范字节写入 `Decision.Value`。不再解释来源，也不重新投票 |
| 代表源 | 获胜簇里本次捕获顺序的第一个支持源。未迁移方法：Value 就是它的原始载荷（4e 删减除外）。已迁移的数量和成功 DATA：Value 是规范编码，这个人只作审计锚点 |
| 域外字段 | 已准入载荷里有、但编码器没有输出的字段。未迁移对象还没有这层编码器，这些字段仍留在代表源原文里 |

## 4. 受保护结果域

### 4.1 构成

域由三部分组成，不能直接等于今天的比较键：

1. 比较键覆盖的字段。
2. 受约束的字段：被请求绑定，或被已经验证的内容承诺约束。区块头受 header hash 约束，交易签名内容受交易根约束，withdrawals 受 withdrawals root 约束，uncle 受 uncle hash 约束。
3. 派生字段：由前两类按公开、确定的规则在本地算出。例如交易 hash，以及区块内交易的 `blockHash`、`transactionIndex`。

不在域内：厂商扩展；没有佐证的上下文字段，例如 revert 的 code、message；来源自述但没有约束的字段，例如区块里的 `from`、`size`、`totalDifficulty`。`from` 的口径见第 14.3 节：默认留在域外。没有测量不能进域。未迁移对象（含区块）的 Value 仍是代表源原文，这些字段还会出现在结果里，要到阶段 3b 才从区块 Value 去掉。数量和成功 DATA 已是规范编码。

区块的比较键可以仍是 `number|hash`。头、交易、withdrawals 和 uncle 只有在准入已经核对过承诺之后，才进入域。

### 4.2 判定顺序

一个字段能不能进入 Value，按这个顺序判断，并在编码器注释里写明依据：

```text
字段在比较键里？                         → 进域
不在，但被请求绑定或已验证承诺约束？     → 进域
不在，但能由域内内容按公开规则派生？     → 进域（本地计算，不取来源给出的值）
都不是                                   → 域外：Extensions / Raw / Uncertain
```

到第四步就停。不为这个字段再写一条专用标准，也不把第 14 节变成字段登记表。方法契约可以记下某个字段走了哪一步，那是编码器测试的依据，不是第二份标准。下一个字段若这四步已经能判定，就不要再开一节。

### 4.3 逐字节相等检查什么，不检查什么

支持源各自编码后的字节必须相同。这保证三件事：Value 不取决于挑中哪个来源；不存在把 A 的 `x`、B 的 `y`、C 的 `z` 拼成谁都没返回过的对象；比较键不需要可逆。

反例：A=`{x:1,y:1,z:2}`，B=`{x:1,y:2,z:1}`，C=`{x:2,y:1,z:1}`。逐字段 2/3 多数会得到 `{x:1,y:1,z:1}`。这是 0024 禁止的拼装，本文也不做。

逐字节相等不能单独证明编码器没有丢掉应受保护的字段。删掉一个两边不一致的域内字段，输出也会相等。所以还要有：

- 方法测试列出每个输出字段的进入依据：比较键、请求绑定、内容承诺或公开派生规则。
- 变异测试：拿掉或改掉一个域内字段后，EncodeCheck 必须失败，不能靠省略制造相等。
- 往返：对每个支持源，`typed(raw_i)` 等于 `typed(Encode(raw_i))`。派生字段另查 `derive(protected(raw_i))` 等于编码结果里的派生字段。

这些是普通表驱动测试，不新增 DSL，也不新增 `suite.RunAll`（0040）。运行时做逐源编码和簇内字节比较。完整字段表由方法测试和 golden 守住。

来源给出的派生字段，规范编码后必须等于本地按公开规则算出的值。不相等，或方法要求该字段存在却缺失，则这份载荷自身不成立：EncodeCheck 失败，记 Fault，留在分母里，不投票。不得改写成算出的值后再计票。通过检查的来源，输出仍用本地算出的规范值，只统一拼写。

两人写下同一个错误 transaction hash，且它和签名载荷对不上时，两份都不成立。不能因为彼此相同就输出本地 hash 并标成 Corroborated。`0xABC` 与 `0xabc` 是同一枚 hash，继续投票。transaction hash 只约束签名载荷；`from`、`blockHash`、`blockNumber`、`transactionIndex` 不在 `tx.Hash()` 里。这些字段在被定义成派生或承诺字段之前不进 Value；一旦定义，对不上时同样排除整票。

## 5. 规范输出规则

Execution API 给出形状和约束，给不出唯一字节。数量、成功 DATA、`net_version` 的十进制和 `net_peerCount` 的折合数量，规则正文只在 `rpc/internal/wire/encode.go` 的包注释里。本文不保留第二份标量规则。

对象、地址、浮点和缺失与 null 还没有落地，不在那份注释里：

| 项 | 规则 | 依据 |
|---|---|---|
| 地址 | 小写，不用 EIP-55 大小写 | spec 允许两种大小写；geth JSON 输出为小写 |
| byte 类字段（`v`、`yParity`、`type`） | 按 QUANTITY 规则 | 与 geth 输出一致 |
| 对象键顺序 | 由编码器的 Go struct 字段顺序固定；不承诺语义顺序 | golden 锁定 |
| 空白 | 紧凑编码，不带空白 | `encoding/json` 默认 |
| HTML 转义 | 关闭（`SetEscapeHTML(false)`） | 避免 `<>&` 被改写 |
| 浮点（`gasUsedRatio`） | 用 `json.Number` 读入十进制原文，输出 Go 最短 round-trip（`strconv.FormatFloat(f, 'f', -1, 64)`）。只有最短形式的十进制值与原文不同，才不可准入 | 避免 float64 静默改数；尾随零不是错误 |
| 大整数 JSON 数字 | 禁止 float64 解码，一律 `UseNumber` | 同上 |
| 缺失与 null | 可选字段缺失即省略；只在方法契约允许 null 的位置输出 `null`；不把 null 补成空值 | 0022 |
| 结果为 JSON null | 按原样输出 `null`（对象查询的 NotFound） | 0022 |

浮点要按十进制值判断，不能按“字面量是不是已经最短”判断。

- `0.50`、`0.5000` 和 `0.5` 是同一个数。两者都通过 EncodeCheck，投同一票。Value 里写 `0.5`。`0.10` 与 `0.1`、`1.0` 与 `1` 同样处理。
- `0.30000000000000001` 的最短形式是 `0.3`，float64 相等，但十进制原值变了。这个来源 EncodeCheck 失败，记 Fault，留在本轮分母里，不投票。它不是 `0.3` 的另一种拼法。
- `1e-1` 与 `0.1` 的十进制值相同，按可规范化处理，输出 `0.1`。

范围检查仍然属于 Admit。`gasUsedRatio` 超出 `[0,1]`、NaN、Inf，在编码问题之前就已经是无效响应。

## 6. 处理流程

### 6.1 总顺序

投票分母和门槛在调用开始时固定（0025、0028、0029）。超时、无效响应、EncodeCheck 失败都不缩小分母，也不放低门槛。

```text
collect（按捕获成员顺序）
  → jsonsafe.Validate
  → Admit（必填、请求绑定、已验证承诺）          现状
  → EncodeCheck（逐源；失败则 Fault，不进入分簇）  已定
  → 比较键（只用通过 EncodeCheck 的来源）         已定
  → Resolve（分簇、门槛、Status，只此一次）       已定
  → EncodeCommit（提交已相等的规范字节）          已定
  → Provenance、Extensions
  → typed decode（只读 Value）
```

Admit 仍在 EncodeCheck 之前。先编码、再比对，指的是比对投票之前完成编码检查，不是把 JSON 合法性或必填检查挪到编码之后。

EncodeCheck 属于准入的效果：单源无法给出不改变原值的规范编码时，记 Fault，不贡献支持，来源仍占着分母里的位置（0020）。然后只用剩余的可编码来源做唯一一次 Resolve。不要先 Resolve，再因为编码失败把人拿掉重算门槛。

具体场景：三人，门槛 2。A 的 `gasUsedRatio` 是 `0.5`，B 是 `0.50`，C 是 `0.30000000000000001`。C 在 EncodeCheck 失败，不投票，分母仍是 3，门槛仍是 2。A 和 B 的规范值都是 `0.5`，进入同一簇。不能把分母改成 2，也不能先宣布成功再收回。若只有 A 能编码，支持数是 1，这一次就是失败，不能缩成分母 1 之后变成功。

### 6.2 比对用什么

比对键仍然是方法自己的比较键，不是最终那一整段 JSON。

| 方法 | EncodeCheck | 比对 | EncodeCommit |
|---|---|---|---|
| 数量、成功的 DATA（chainId、balance、nonce、code、storage、成功的 eth_call 结果） | 规范编码就是比较键 | 比这份规范字节 | 同一份字节。三步看见的是同一结果 |
| Median | 不能规范编码的数量先排除，不进入样本 | 在剩下的样本里按现有中位数规则取一次 | 把取中的那个数写成规范 QUANTITY。Status 维持 Selected，不因为编码一致升成 Corroborated（0012） |
| feeHistory | 数量按 QUANTITY；`gasUsedRatio` 按第 5 节。比较键里的比例必须使用规范十进制，不再经 `[]float64` 把不同原值折成同一键 | 方法现有的 feeHistory 键，其中的比例已是规范十进制 | 获胜簇的规范对象。簇内字节不同则按 6.4 |
| eth_call 的 data 维度、区块 | 只回答受保护内容能不能编码。此时还不知道 4e 会不会扣下 data，也不在这里重做区块承诺 | data 维度或 `number\|hash` 维持现行键。不用整段最终 JSON 投票 | 裁决之后才生成最终字节。data 未达门槛时由编码器省略，不再用 `deleteObjectField` 重序列化 |
| Local | 被选中的那一个来源能否编码 | 不改 Local 的选取 | 该来源的规范编码。Status 仍为 Selected |
| Forward（未注册） | 无编码器 | 现状 | 保持原值，Status 为 Forwarded，不作受保护声明 |

`eth_call` 信封和区块重编码的口径见第 14.2、14.3 节。方向已锁，阶段 3a、3b 之前不实现。EncodeCheck 不得把 code、message 或未约束的区块字段编进受保护结果。阶段 1 接入的数量和成功 DATA，比较键就是规范字节：EncodeCheck、比对、EncodeCommit 看见同一串字节。eth_call 的 code、message 和未迁移对象的 Value 仍是代表源原文。

### 6.3 公共检查与方法编码器

公共代码只做三件事：对通过检查的来源调用该方法的编码器、要求获胜簇的规范字节相等、装配代表源和 Provenance。

`func(json.RawMessage) (json.RawMessage, error)` 够用的范围只是数量和 DATA 这类单载荷标量。`eth_call` 的 data 维度、区块的请求绑定和承诺，都要额外输入。这两类的函数形状随第 14.2、14.3 节的口径走。阶段 3a、3b 之前不实现。不在本文假装已经有一个万能签名。

编码器是可选能力，挂在方法装配处。没有编码器注册表。未声明编码器的已注册方法，在迁移期间保持原值。迁移状态记在方法规则索引的 result 列，不靠一套新的运行时契约语言。`ResultKind` 不作为公开字段，见第 14.4 节。

### 6.4 簇内规范字节不一致

同一个比较簇里，通过 EncodeCheck 的支持源却得到不同规范字节时：

- 这一次 Resolve 的结果是 Unknown，返回 `ErrMalformedResponse`，原因是 `result encoding disagrees within cluster`；
- 不改选另一个来源的原文，不逐字段取多数，不把不一致的来源剔除后再投一次；
- 各支持源的编码摘要写入 Faults，用来区分是键太窄还是编码器越界；
- 这是代码缺陷，必须有测试，不是正常的来源分歧。

非获胜簇里的同类不一致，口径见第 14.1 节：记入 Faults，不否决已达门槛且自身字节一致的获胜簇。不能靠忽略它来触发第二次投票。获胜簇自己不一致仍按本节 Unknown。

### 6.5 证据通道

```text
Representative   第 7 节选出的来源
Raw              该来源已准入的原始载荷
Admission        该来源的准入证据（原先 Decision.Evidence 里的 uncle 头等）
Withheld         4e 扣下的字节
```

Raw、Admission、Withheld 和 Representative 必须是同一个来源。禁止 Value 取最后一个、Evidence 取第一个、Withheld 再取另一个观察。未迁移方法的 Value 仍是这个人的原始载荷（4e 删减除外）。已迁移的数量和成功 DATA 不再从某一个人的原文里挑；这个人只提供上面这一条证据通道。原文不写入 Provenance。

Provenance 常开，不依赖 Uncertain。Uncertain 仍是 opt-in 的全量原始捕获（0034）。迁移期间可以保留 `Decision.Evidence`，语义收窄为准入证据，最终并入 Provenance。证据预算若存在，耗尽只能减少审计材料，不能改变 Admit、Resolve 或 Status。

默认只保留一个代表源的原始载荷。其他来源的完整原文仍走 Uncertain。大对象超限时的口径见第 14.6 节：可见标记、原始长度和 SHA-256，不保留截断后的字节。触发截断的字节上限没有数字，仍开放，不挡住阶段 0。

### 6.6 Decision 上的结果

| 字段 | 现状 | 设计后 |
|---|---|---|
| `Value` | 某一个来源的原始字节；4e 时是删减后的重序列化 | 受保护结果域的规范编码 |
| `Evidence` | 准入证据加 4e 扣下的字节，可以和 Value 不同源 | 过渡字段，最终进入 Provenance，且与代表源同源 |
| `Extensions` | 指纹依赖代表源拼写 | 指纹取规范 Value；域外字段是 Raw 相对 Value 的路径差集 |
| `DeletedFields` | 4e 删除的字段 | 保留，语义不变 |
| `Representative` | 没有这个字段。Strict、CallRevert 在簇内取最后一人，Median 取等值样本里的第一个，而且可以和 Evidence 不同源 | 阶段 0 起就写入：Strict、CallRevert、Median 在带出获胜簇 Value 时记下捕获顺序第一人。Local 与 Forward 保持零值。规范编码落地后这个人只标审计锚点 |

「设计后」一列里，`Value`、`Evidence`、`Extensions` 三格写的是规范编码落地后的终态。阶段 0 不把它们改成那一列，但仍然要改 Value 和 Evidence 的来源：从「Value 取簇内最后一人、Evidence 取另一个有证据的人」改成捕获顺序第一人，Withheld 也是这一人。改完之后，阶段 0 的 Value 字节仍是这个人的原始载荷（4e 删减除外），不是规范编码。阶段 1 的数量和成功 DATA 把 Value 写成规范编码；未迁移方法的 Value 仍是这个人的原始载荷。`Representative` 从阶段 0 起写入。Extensions 指纹和 Provenance 不在阶段 0 或这一刀开工。

域外字段沿用 `corroborated_extension`、`provider_residual`、`conflict_field`。`AggregateFields` 只处理这些域外路径，并按规范化后的叶子值分组，不再按原始叶子字节分组。`projection.StandardFields` 是与编码器平行的第二份字段表。口径见第 14.3 节：它不作为区块 Value 的依据。阶段 0 不删。数组路径不能再因为匹配不到清单而被当成扩展或 Missing。

## 7. 代表源

- 定义：获胜簇中，本次调用捕获成员顺序里的第一个支持源。
- 这个顺序和投票成员一起在调用开始时捕获（0025）。含义就是调用方配置的顺序。
- 生产路径已经按成员下标写入。Policy 单测和 replay 必须传入同一个捕获顺序。replay 以 `index.json` 的来源顺序为准，并在 `expect.json` 记录 `ordinal`。
- 不按 `Source.ID` 排序。已迁移标量的 Value 与代表源拼写无关；再按端点 URL 的拼写选审计锚点，会变成第二条规则。未迁移方法的 Value 仍是这份代表源原文，不能用「与代表源无关」把那些方法说成已经规范化。
- Strict、CallRevert、Median 共用 `rpc.representative`。Local 的“第一个健康源”保持为选取策略。Forward 不选代表源。
- 它替换 `cluster` 的 map 覆盖、`call_revert.representative`，以及 Median 里各自的选法。

代表源、Raw、准入证据、Withheld 四者同源。代表源的准入证据为空时就保持空，不得改用簇内后一个来源的证据。测试若要证明“输入任意重排，规范 Value 仍相同”，必须显式重排后断言编码，不能借机改掉生产中的成员优先级。

阶段 0 不改「Value 取哪一簇」，只把该簇内部从最后一人改成捕获顺序第一人。`eth_call` 的 4e 仍按现实现选簇：data 有具体值时，Value 来自 data 排名第一的那一簇（人数多的在前，人数相同再按键排序）；这一簇没达到门槛时，仍用它的信封，再删掉 data。全部在 data 上弃权时，才用执行簇。代表源是这一簇的第一人。Withheld 只放这一人扣下的 data，不收集其他来源的 data。

未迁移方法的 Value 仍是代表源的原始载荷（4e 删减除外）。已迁移的数量和成功 DATA 上，代表源只决定审计锚点，不决定 Value。未迁移对象因此还不能把 Corroborated 写成语义安全：`from`、`size` 仍会随区块原文出现，要到阶段 3b 才从 Value 去掉。

阶段 0 的 `Decision.Representative` 用现有的 `SourceRef`，放在 Evidence 旁。失败但仍带出获胜簇 Value 的路径也要填。Local、Forward，以及本地的 `ResolveLocal`，保持零值。

## 8. 文件组织

不新增顶层包。标量规则放 wire，对象字段表留在对象 slice，方法文件只装配。

```text
rpc/internal/wire/encode.go
    标量、JSON token、第 5 节的字节规则

rpc/internal/objects/<object>/encode.go
    该对象的字段、承诺绑定、派生字段、typed 模型
    receipt、transaction、log、feehistory 随阶段 2
    block 等到区块决策，不提前新增第二份区块字段表

rpc/methods/eth_*.go
    把 admission、比较键、resolver、encoder 装在一起
    只被一个方法使用的形状留在方法文件

rpc/result.go
    EncodeCheck、EncodeCommit、representative、Provenance 装配
    只检查编码结果相等，不解释某个 Ethereum 字段

aggregator
    只编排。不解释 Value，不维护字段表
```

禁止：

- 与编码器平行的 `StandardFields`、YAML 或运行时 schema；
- 从比较键反推所有方法响应的万能编码器；
- 用代表源原文加 mask 白名单当作规范形式。mask 比域宽，区块的 `size` 就在 mask 里，却不受承诺约束；
- aggregator 了解某个方法的字段语义；
- 每个方法复制一套 Resolve、Provenance 或 evidence 类型。

比较键继续用现有的 `normalize.go` 和 `prune.go`。`prune.go` 不做序列化器。对象编码和准入共用同一个 typed 模型。派生字段只在编码器里计算一次，typed 层只解码 Value。

新增文件限于 `rpc/result.go`、`wire/encode.go`，以及每个需要编码的对象 slice 一个 `encode.go`。再往外拆之前先检查是不是越界。

## 9. 测试与进入条件

每个方法在进入 canonical 交付前，除现有路线图的条件外，还要有：

1. `method-contracts.md` 写明受保护结果域：每个进域字段的依据，以及域外字段去哪。依据只允许是第 4.2 节的四步之一，不另写字段标准。
2. 编码器，以及第 4.3 节的同余、变异和往返测试。
3. EncodeCheck 失败、获胜簇编码不一致、域外字段来源这三类测试。
4. golden 钉住规范 `value` 字节、`representative`、每个来源的 `ordinal`、Provenance 和域外字段。`value` 用原样 JSON 还是 base64，实现时二选一并在样本说明里固定，不改变断言的字节。
5. 更新 golden 必须带 diff 审阅。
6. Policy 层，以及 Aggregator 到 typed 读取的离线回放。
7. 大对象编码的分配和内存测量。口径见第 14.5 节：测量之前和之后都对每个支持源做全量 EncodeCheck，不抽样。不写未测量的配额。这一条随规范编码落地，不是阶段 0 的出口。

位置遍历不能合成一句。规范编码落地之后：把表示变体放到每一个 pool 位置，`Decision.Value` 逐字节不变，typed 结果不变，`Representative` 按第 7 节变化。阶段 0，以及仍返回原文的方法，没有「换位置则 Value 字节不变」：把另一份拼写挪到捕获顺序第一位，Value 就变成那一份原文。这时要稳定的是规则，也就是第一人，并且与 Evidence、Withheld 同源。变体包括大小写、`0X`、键序、空白、JSON 转义和域外字段。

方法规则索引增加 `result` 列，取值为 `canonical`、`raw` 或 `redacted`。迁移完成后，已注册方法不得再是 `raw`。索引只检查有没有注册编码器以及 result 列，不解释字段，也不生成测试（0040）。

必须保留的固定用例：revert code 随配置顺序变化、JSON 转义导致 typed 成功或失败、锚点 hash 的 `0X` 拼写、区块键外字段进入了 Corroborated Value。阶段 0 之后，前三个里“选中哪一份原文”必须稳定；“转义代表源让 typed 失败”要保留为已知缺口，直到该方法改为从规范 Value 解码，不能把断言放宽。

## 10. 采纳时要修订的既有表述

| 位置 | 现文 | 改为 |
|---|---|---|
| 0039 规则 4e | methods that do not declare the projection still return that source value | 已注册且已迁移的方法返回受保护结果域的规范编码 |
| 0039 第 2 类 | They stay on the winning payload as uncorroborated context | 这一刀不改。未迁移对象上它们仍留在 Value 里 |
| `architecture-record.md` 的 Value 句 | 写入获胜簇的原始值，不改字段 | 已迁移标量写规范编码；未迁移 Strict 仍写捕获顺序第一人的原文 |
| `architecture-record.md` 第 127 行 | Normalize 不修改胜出 raw payload | 这句保留。Normalize 仍不改写原始载荷。另加一层：Encode 生成 Value |
| `rpc.Decision.Value` 注释 | Generic Strict copies one admitted source value | 已迁移方法写规范编码；未迁移 Strict 仍是捕获顺序第一人的原文。原文不在 Provenance |
| pipeline 1.1 目标语义第 2 条 | 未声明的方法仍返回来源原值 | 迁移期间 result 列为 `raw` 或 `canonical`。迁移完成前仍允许 `raw` |
| `method-contracts.md` 第 0 节 | 取簇内最后一个池序成员的原始回答 | 已迁移方法的 Value 是规范编码；其余 Strict 方法仍是捕获顺序第一人的原文 |
| `eth_getblockbyhash.go` 注释、method-contracts byHash 行 | 整对象比较 | 这一刀不改，也不写成「内容由承诺约束」 |

上表是 0041 采纳时要改的句子，不是阶段 0 的改动清单。阶段 0 只改两处选源事实，而且必须和代码同一批改，不能先改文档：

- `method-contracts.md` 第 0 节：「获胜簇的 Value 取簇内最后一个池序成员的原始回答」改为捕获顺序中的第一个。Value 仍是该来源的原始回答，不是规范编码。
- 同文件 byNumber 归一化行：「获胜簇 Value 取簇内最后池序成员的原始对象」同样改为捕获顺序第一个。

阶段 0 的那两处已经落地。0041 这一刀改成事实的只有这些句子：`method-contracts.md` 第 0 节、`Decision.Value` 注释、`architecture-record.md` 里 Normalize 仍不改原文并补上 Encode、pipeline 1.1 的迁移期 result 列。0039 第 2 类没有改成已经离开 Value。byHash 没有改成「内容由承诺约束」。原文没有写入 Provenance。RV1 已在 48e2c64 验收；下一刀的任务只写在路线图 RV2a，不在这里展开。

0020 不用改。单源没有规范形式时记 Fault 并留在分母，就是 0020 已经要求的“不得静默进入受保护业务结果”。

0039 的 4e 仍然只删除未达门槛的字段，不新增、不替换、不跨源拼接。本文把“未受保护的内容不进 Value”从 revert data 推广到域外字段。推广本身要等 0041，不能当成缺陷修复顺手改掉 0039。

`method-verification-pipeline.md` 第 6 节的可缺席字段模式维持未采纳。0041 不把它收成框架。

## 11. 分阶段落地

| 阶段 | 内容 | 需要决策 | 出口 |
|---|---|---|---|
| 0 | 不引入规范编码，Value 仍是某一个来源的原始载荷。但 Strict 和 CallRevert 选中的那一份会从“簇内最后一个”改为“捕获顺序第一个”，所以原文字节会变，不能写成“不改 Value”。同时让 Value、Evidence、代表源同源，Decision 记下代表源。StrictHeadAnchor 的锚点 hash 改为规范小写，与 FinalizedHeadAnchor 一致。这个 hash 是 EthGuard 发给所有来源的请求参数，不是返回值规范化 | 否 | 锚点拼写用例通过；Value 与 Evidence 同源测试通过 |
| 1 | 立 0041。增加 `rpc/result.go` 和 `wire/encode.go`。数量类和 DATA 类接入第 6 节的顺序 | 0041 | 这些方法的 result 列为 canonical；同余测试通过；typed 层改读 Value |
| 2 | receipt、transaction、logs、blockReceipts、accessList、feeHistory。Extensions 改为差集；golden 钉 value。feeHistory 的比例按第 5 节，不再用 float64 比较键 | 否，前提是 0041 已覆盖对象编码 | JSON 转义用例改为从规范 Value 解码后通过 |
| 3a | eth_call 信封：code 与 message 留在代表源，不进受保护域，不合成信封 | 方向已锁，见第 14.2 节。阶段 0 不实现，也不因此立 0041 | 阶段 0 的出口不包含这一行 |
| 3b | 区块 Value 只重编码已经核对过承诺的内容。`size`、`totalDifficulty`、厂商字段和 `from` 不进 Value。`from` 测量之后要不要改成派生字段仍开放 | 方向已锁，见第 14.3 节。阶段 0 不实现 | 未迁移的区块 Value 仍会带上这些字段。这是已知缺口，不能写成已经语义安全 |
| 4 | 索引断言已注册方法没有 `raw` | 否 | 第 9 节的断言生效 |

每个阶段单独提交。出口是可复现的 `go test`、失败路径和 golden diff。

阶段 0 选出的这个人，后面的阶段继续用同一个函数。未迁移方法仍由它决定 Value 用哪一份原文。已迁移标量的 Value 不随这个人的拼写变化。Evidence 和 Withheld 必须是同一人。不能把未迁移方法写成「只是审计锚点」。

## 12. 明确不做

- 不从比较键反序列化出响应，再当作 Value。数量和 DATA 的键本身就是规范字节，那是同一份输出，不是反推。
- 不从 `AggregateFields.Common` 逐字段拼对象。
- 不把“代表源原文经 mask 投影”定义成规范形式。
- 不新增方法契约 DSL、字段清单语言或编码器注册表。
- 不把可缺席字段收成通用框架。
- 不逐方法、逐字段定档。进域与否只走第 4.2 节。
- 不因编码失败缩小本轮分母或降低门槛。
- 不在 Resolve 之后再裁决一次。
- 不把 Median 或 Local 的 Selected 升成 Corroborated。
- 不改比较键的含义来迁就编码器。键太窄时扩大的是域的声明和编码器，不是临时换一份额外 JSON 去投票。
- 不把 EthGuard 改成后台共识组，也不在网关里把 `latest` 重写成块号。0026 不跨调用缓存引用，0033 把新鲜度和一致分开；高度对齐不能替代本次调用的裁决。
- 不因 header hash 或 `transactionsRoot` 已经核对通过，就把 `from`、`size` 或整份代表源 JSON 当成受保护结果。
- 不新增最终响应投影层，也不公开 Strict/Compatible、污点标记、字段掩码或投影器接口。
- 不把未测量的字节上限或编码时限写进本文。输入界限仍用现有的 JSON 限制。

## 13. 外部参考

- geth 的 `RPCMarshalBlock`、`RPCMarshalHeader` 和 `hexutil` 只对照输出形状。不据此宣称兼容。
- execution-apis 提供字段形状和模式，不提供第 5 节的字节规则。
- proxyd 和 eRPC 返回单个后端的原始响应。它们信任后端；EthGuard 不信任。这是差异，不是对它们的否定。

以后每加入一个外部参考，只回答两件事：它是否改变第 5 节的某条规则，以及是否改变第 4.2 节的判定。都不改变，就只留在背景里。

### 13.1 2026-09-27 的两份外部意见

产品选择已锁进第 14 节。这一节仍只作对照：不改变第 5 节的字节规则，也不改变第 4.2 节的四步。不写入 `docs/decisions.md`，不立 0041，不授权阶段 1 及以后的实现。

两份意见争的是同一条边界：`Decision.Value` 到底是受保护结果，还是一份真实上游响应。对照之后，产品不改。EthGuard 仍是平权多源客户端。成员和分母在一次调用开始时冻结（0025）。多源一致只是提供商容错和防投毒证据，不是链共识。

proxyd 在后台轮询各后端的 `latest`、`safe`、`finalized`，算出共识组，再把客户端的 `latest` 重写成具体块号；错误的 `code` 和 `message` 原样回传。它减少的是时间窗口里的高度差。这里不采用。引用不跨调用缓存（0026），新鲜度与一致分开（0033）。它不改变第 5 节，也不改变第 4.2 节。

eRPC 用语义键分簇，达到门槛后返回获胜簇里一份上游原文，落选者只记遥测；区块看 `transactionsRoot`，不对每笔交易做 `ecrecover`。现状的 `Decision.Value` 就是这种簇内原文。提案要离开它，因为原文不稳定，而且会把没有承诺的字段带进 Corroborated。落选簇不否决胜者，这一点与第 14.1 节相同。它不改变第 5 节。

有一句不能当成依据：header hash 或 `transactionsRoot` 通过，就等于锁住了 JSON 里的 `from` 和 `size`。交易 RLP 不含 `from`；`size` 不是区块头字段。块哈希和交易根都通过时，代表源仍可以写下另一个 `from` 或另一份 `size`。场景在第 14.3 节。因此不把「根通过就透传整份原文」定义为 Value。

另一份意见把边界认对了：Status 只描述 Value；`code`、`message`、`size`、`from` 不进受保护域；获胜簇自己的规范字节必须相同（第 6.4 节）；证据走 Provenance。它随后加出来的第二套产品不采纳：最终响应的 Strict/Compatible、污点标记、字段掩码、投影器接口、对外的 `ResultKind`、合成出来的错误信封，以及没有测量的 64KiB、2ms、8MiB 配额。这些不改变第 4.2 节，也不进入第 14 节。

执行仍按载荷，不按字段开清单。标量和成功的 DATA，规范编码就是比较和 Value。`eth_call` 的 revert 只保护 data，`code` 与 `message` 留在代表源（第 14.2 节）。区块和带承诺的对象，Value 只重编码准入已经核对过的承诺内容（第 14.3 节）。每个可能计票的来源都对受保护域做 EncodeCheck（第 14.5 节）：不为落选者的拼写单独打分，不把将要留在域外的字段再序列化一遍，不抽样，不新增配额表。

## 14. 已锁定口径

2026-09-27 锁定。它是阶段 0 及后续阶段的工程依据，不再标成不定档。仍不写入 `docs/decisions.md`，不立 0041，也不把第 11 节的阶段 3a、3b 变成可以开工的出口。阶段 3a、3b 的方向就在下面，阶段 0 不实现。

这不是在给每个方法的每个字段定标准。

一个字段进不进 Value，只走第 4.2 节的四步。走到“域外”就停，不为它再写一条规则。方法契约可以记下某个字段走了哪一步，那是编码器测试的依据，不是第二份标准。下一个字段若四步已经能判定，就不要再开一节，也不要进本节。

下面七条都不是字段表。14.2 和 14.3 提到的 code、message、`size`、`from`，只是第 4.2 节的例子：前两个停在域外，区块则沿用第 4.1 节已经写过的承诺，不新列一份区块字段清单。

### 14.1 非获胜簇编码不一致

口径：做法乙。落选簇里规范字节不一致，记入 Faults，不否决已经达到门槛、并且自己字节一致的获胜簇。不第二次投票，不缩小分母，不改变这次已经做出的 Status。

五人，门槛 3。A、B、C 的键是 `100|0xaaa`，规范字节都是 V1。结果是 Corroborated 的 V1。D、E 的键是 `100|0xbbb`，规范字节是 V2 和 V3，只写入 Faults。

做法甲不是当前口径。那一枝是任何一个簇不一致就整次 Unknown，落选分叉上的一份坏编码就能否决已经成立的 V1。

获胜簇自己不一致，没有这条口径。五人，门槛 3。C、D、E 同键但规范字节不同，A、B 只有两票。整次仍是 Unknown，返回 `ErrMalformedResponse`，原因是 `result encoding disagrees within cluster`。不能丢掉 E，再让 C、D 以两票当选。

五人，门槛 4，只有 A、B、C 三票一致，D、E 同键但字节不同。失败是普通未达门槛，不是上面这个编码错误。D、E 可以记入 Faults，不能把分母缩成 3。

这是一次调用怎么收场，不是某个字段的标准。第 13 节里「落选簇有致命不一致就整次失败」不采纳，那就是做法甲。

### 14.2 `eth_call` 的 code 与 message

口径：做法甲，已锁定。code 与 message 不进受保护域，也不按 geth 规则合成信封。做法乙（合成固定信封）不是未决项。要改成乙必须另立决策，不能在实现时选择。

Raw、Admission、Withheld 与代表源是同一个来源。两段时期不要写成同一句：

- 成功 DATA 的 Value 已是规范编码，不含 `ok|`。revert 的 Value 仍是这个人的整份原文（4e 删减除外），所以 code 与 message 还在 revert 的 Value 里。下面例子里，顺序 A、B、C 时 Value 的 code 是 A 的 3；换成 B、A、C 时变成 B 的 -32000。这是选源结果，不是又一个未决项。
- 阶段 3a 之后：Value 是受保护域的规范字节。簇内规范字节已经相等，代表源不再决定 Value。code 与 message 只留在这个人的 Raw 上。

三人，门槛 2。三份都是 revert，data 都是 `0xdead`，data 达到门槛。A 的 code 是 3，message 是 `execution reverted`。B 的 code 是 -32000，message 是 `execution reverted: revert`。阶段 3a 之后，Value 是 revert 加 data `0xdead`，两种捕获顺序下逐字节相同。顺序 A、B、C 时，代表源 Raw 上是 A 的 3 和 `execution reverted`；改成 B、A、C 时，Raw 上变成 B 的 -32000 和 B 的句子。阶段 3a 之前，revert 的 Value 仍是代表源整份原文，code 与 message 随第一人改变。成功结果不是这句：它的 Value 已是规范 DATA。

data 未达门槛时，阶段 0 的 4e 仍用代表源信封，再删掉 data。code 与 message 还在这份 Value 里。不从别的来源补 data，也不合成 message。阶段 3a 才改为由编码器省略 data，并且 code 与 message 不进入 Value。

这不是给 `eth_call` 的错误对象逐字段定标准。code 与 message 没有通过第 4.2 节，所以停在代表源上。data 仍用现有的比较键和 0039 规则 4e。第 13 节里的最小合成信封不采纳，那是做法乙。

### 14.3 区块重编码，以及 `from`

口径有两句。重编码方向已锁，阶段 0 不实现。`from` 测量之后的归属仍开放。

区块 Value 按做法乙重编码。只收第 4.1 节已经写过的、准入核对过承诺的内容：头受 header hash 约束，交易签名内容受交易根约束，withdrawals 受 withdrawals root 约束，uncle 受 uncle hash 约束。不另列一份根字段清单。`size`、`totalDifficulty` 和厂商字段不进 Value，留在代表源 Raw 相对 Value 的差集里。

重编码落地之后：三人，门槛 2。三份的 `number|hash` 相同，捕获顺序 A、B、C，代表源是 A。A 的 `size` 是 `0x100`，B 的 `size` 是 `0x200`。比较键不分叉。Corroborated 的 Value 里没有这两份 `size`，也没有只在 A 上出现的 `totalDifficulty`。区块重编码还没有做，那两份 `size` 仍留在代表源原文里。

`from` 先不进域。交易 hash 约束不了它，第 4.2 节的派生一步现在不成立。A 写 `0xaaa`、B 写 `0xbbb` 时，B 不因此被排除，两份都不进 Value。本地能够恢复出 `0xaaa`，也不把一次失败改成成功。代表源 Raw 里可以看见 A 写下的 `from`，那不是第二条投票。要不要在 CPU 测量之后把它定义成派生字段，仍开放。默认留在域外。没有测量就不能进域，也不能为此把整票判掉。未迁移的区块 Value 仍是代表源原文，所以 `from` 和 `size` 还会出现在这些 Corroborated 结果里。数量和成功 DATA 没有这些字段。这是已知缺口，要到阶段 3b 才从区块 Value 去掉。现在不能把区块写成已经语义安全。

第 13 节的透传意见在这里不成立。三人，门槛 2。三份 `number|hash` 相同，交易根也通过。A 的 `from` 是 `0xaaa`，B 的是 `0xbbb`。捕获顺序 A、B、C 时，若把 A 的原文当作 Value，Corroborated 里的付款人只是 A 的断言；顺序改成 B、A、C，就变成 `0xbbb`。`size` 同样可以在块哈希不变时一个是 `0x100`、一个是 `0x200`。根核对通过，这两项仍停在第 4.2 节第四步。

`projection.StandardFields` 是与编码器平行的字段清单。它不作为区块 Value 的依据。代码里先不删，阶段 0 也不动它。

### 14.4 名字与 `ResultKind`

口径：证据通道叫 Provenance。`Decision.Evidence` 只在迁移期作为别名，最终并入 Provenance，并且和代表源同源。`ResultKind` 不出现在公开的 `Decision` 上。方法规则索引的 result 列仍只记录迁移状态，取值照旧是 `canonical`、`raw` 或 `redacted`。

三人，门槛 2。`eth_call` 的执行维度达到门槛，data 只有一票，被 4e 扣下。调用方看 Status、Value、`DeletedFields` 和 Provenance。不用一个 `canonical` 或 `redacted` 概括整份结果。

这是通道的名字，不是新的字段分类。第 13 节不为此再加最终响应投影，也不把 `ResultKind` 公开到 `Decision` 上。

### 14.5 每个支持源都编码

口径：做法甲。测量之前和测量之后一样，每个支持源都做完整 EncodeCheck。不抽掉某个来源，也不改成只抽大字段。

五人，门槛 3。A、B、C 比较键相同。C 也要编码。C 的规范字节与 A、B 不同，整次按第 6.4 节 Unknown。C 编不出来，记 Fault，不贡献支持，分母仍是 5。不能因为没编码 C，就把 C 算进支持数。

这是检查范围，不是字段标准。第 13 节否掉只编码代表源：那样会漏掉上面的 C。也否掉为 D、E 的拼写再打一轮分；它们不在获胜键上时，按第 14.1 节记 Fault，不否决胜者。EncodeCheck 只覆盖受保护域，不把留在域外的 `size`、`from` 或厂商字段再序列化一遍。不写 64KiB、2ms、8MiB 这类没有测量的配额。输入界限仍是现有的 JSON 限制。

### 14.6 Provenance 超限

口径：做法甲，已锁定。代表源原文放不下时，不保留前 N 字节。Provenance 放可见标记、原始字节长度和 SHA-256。Value、Status、代表源序号不变。截断体不得再进入 Admit、EncodeCheck 或 Resolve。

五人，门槛 3。五份区块各 2MB，代表源是 A。B、C、D、E 的全文本来就不在常开 Provenance 里，仍只走 opt-in 的 Uncertain。A 的原文若超过限额，这条 Decision 里不留下半截 JSON，只留下截断标记、长度和 SHA-256。

触发截断的字节上限没有数字，仍开放，不挡住阶段 0。不在这里补一个数。这是容量参数，不是字段标准。

### 14.7 WSS 上的缺失与 null

口径：做法甲。编码器只对还能分清缺失与 null 的输入承诺第 5 节。HTTP 上两者仍分开：缺失就省略，契约允许的 null 才写出 `null`。WSS 上两者在进入观察之前都已变成 nil，这次调用不得声称已经按第 5 节区分过。编码器不把它们收成同一种输出。传输缺口继续单独立项，不在本文修，也不因此停掉 HTTP 路径上的编码讨论。

三人，门槛 2。A 的 revert data 缺失，B 的 data 是 JSON null，C 的 data 是 `0xdead`。走 HTTP 时，A 和 B 在 data 上弃权，C 只有一票，4e 扣下 data，编码输入是“没有 data 字段”。同样三份走 WSS 时，A 和 B 到达编码器之前已经分不清，不能编成同一种输出来假装第 5 节仍然成立。

这不是给 null 再定一种字段标准。第 5 节的规则不动。
