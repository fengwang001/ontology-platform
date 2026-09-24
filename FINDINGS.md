# FINDINGS — 测试结论

## 表一：两个探针下三种做法的返回 vs 本实现

| 做法 | `NOT (secret = 1)` | `secret IS NULL` |
| --- | --- | --- |
| 甲（不可见比较当作假） | 返回全部行（含真实 `secret=1` 的行），泄露 | 按假处理返回空集，说谎 |
| 乙（不可见列当作 NULL） | UNKNOWN 被 WHERE 过滤，返回空集 | 恒真，返回全部行，泄露存在性 |
| 丙 = 本实现 | `ErrInvisibleColumn`，指名 `secret@$/not`，不返回行 | `ErrInvisibleColumn`，指名 `secret@$`，不返回行 |

验证：`filter.TestCounterProbes` 断言两个探针都返回可用 `errors.Is` 判定的
`ErrInvisibleColumn`，且错误携带列名与路径，而不是任何结果行。

## 表二：三十二种穷举组合结论

判定定义（DESIGN.md 第五节）：折叠后树中仍引用任何不可见列 → 拒绝并指名，否则放行。

| 谓词形态 | 引用列 | 放行 | 拒绝 |
| --- | --- | --- | --- |
| `=`（`a = 1`） | {a} | 4 | 4 |
| `NOT`（`NOT (a = 1)`） | {a} | 4 | 4 |
| `AND`（`a = 1 AND b = 2`） | {a, b} | 2 | 6 |
| `OR`（`a = 1 OR b = 2 OR c = 3`） | {a, b, c} | 1 | 7 |
| 合计 | | 11 | 21 |

验证：`filter.TestExhaustive32` 用循环覆盖 8 种可见性掩码 × 4 种形态共 32 种组合，
逐个断言放行 / 拒绝并指名与上表一致；`go test -count=1 ./...` 全部通过。

## 其他已验证结论

- 行裁剪：`filter.TestPruneRow` 断言裁剪行与期望逐键相同、不含不可见列名、与置零值可区分、输入行不被修改。
- 复杂度：`filter.TestSinglePass` 断言访问节点数等于树节点总数；`filter.TestPruneCopiesBound` 断言 1000 列 5 可见时 copies = 5 ≤ 20。
- 报告：`report.TestCanonicalDeterministic` 断言 20 次打乱构造顺序后 `Canonical()` 逐字节相同。
