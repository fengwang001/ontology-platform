# FINDINGS

测试设定：行 R1(secret=1)、R2(secret=2)，角色仅可见 `id` 列。
结论由 `go test -count=1 ./...` 与 `go run ./cmd/demo` 实测得出。

## 表一：探针查询下三种做法与本实现的行为

| 做法 | `NOT (secret = 1)` | `secret IS NULL` |
| --- | --- | --- |
| 甲（不可见比较当假） | 返回 R1、R2（内层恒假，NOT 恒真） | 返回空集（比较当假） |
| 乙（不可见列当 NULL，三值逻辑） | 返回空集（UNKNOWN 被 WHERE 过滤） | 返回 R1、R2（IS NULL 恒真，泄露列存在性） |
| 丙 / 本实现 | 拒绝：`secret at $/NOT` | 拒绝：`secret at $` |

甲的返回集内容依赖 secret 真值（与 `secret = 1` 探针对照即泄露）；
乙把「列存在但被隐藏」与「列不存在」变成可区分，且逐值探测信道
与甲相同。本实现对两个探针均返回 `ErrInvisibleColumn` 并指名列与
路径，不返回任何行（`filter.TestProbesRejected`）。

## 表二：32 种穷举组合结论（8 种可见性掩码 × 4 种谓词形态）

定义（DESIGN.md）：常量折叠后仍存活的不可见列引用才拒绝。模板中
无常量子表达式，故折叠不改变结论；`=`、`NOT` 只引用列 a，
`AND`、`OR` 引用 a、b、c 三列。

| 谓词形态 | 放行数 | 拒绝数 | 放行条件 |
| --- | --- | --- | --- |
| `=`（a = 1） | 4 | 4 | a 可见 |
| `NOT`（NOT a = 1） | 4 | 4 | a 可见 |
| `AND`（a=1 ∧ b=2 ∧ c=3） | 1 | 7 | a、b、c 全部可见 |
| `OR`（a=1 ∨ b=2 ∨ c=3） | 1 | 7 | a、b、c 全部可见 |
| 合计 | 10 | 22 | — |

实测 32/32 与定义一致（`filter.TestExhaustive32` 与 demo 第 9 行）。

## 其他实测结论

- OR 短路例外：`TRUE OR secret=1`、`FALSE AND secret=1` 放行；
  `FALSE OR secret=1`、`TRUE AND secret=1` 拒绝（折叠后引用存活）。
- 谓词检查为单遍遍历：visited 计数 == 树节点总数（多棵树断言）。
- 1000 列行、5 列可见：裁剪拷贝 5 次，上界 4×5=20，与总列数无关。
- 报告在 20 次打乱构造顺序下逐字节相同；同列多路径只报一次、
  路径全部列出。
