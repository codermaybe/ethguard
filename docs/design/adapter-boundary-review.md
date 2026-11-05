# root facade 与 `ethadapter` 对接边界审查

状态：**已废止（superseded，2026-11-05）**。唯一 Client 决策（decisions.md
0065）删除了 `ethadapter`：其 Geth-shaped 方法、`RevertError`、参数转换和
请求绑定迁入 root `ethguard.Client`，与实际声明逐项一致的迁移矩阵见执行
计划的唯一 Client 冲突矩阵（`docs/design/tmp/unique-client-matrix.md`，本地
工作文件、不入库；迁移事实以 decisions.md 0065 与 roadmap 的执行记录为准）。
本文仅保留历史结论：
2026-10-25 的审查曾决定保持 adapter 局部实现、不抽第二层公共转发模块；该
结论随 adapter 表面的删除一并失效，其中的职责划分（类型解码归
`internal/typed`、业务裁决归 root runtime、错误分类保持 sentinel 语义）由
0065 继续承接。
