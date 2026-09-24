# 验证结论

## 表 1：三种做法在两个探针下的行为

| 做法 | `NOT (secret = 1)` | `secret IS NULL` | 结论 |
| --- | --- | --- | --- |
| 甲：不可见比较为假 | 比较为假→NOT 为真→返回全部行；枚举 `NOT(secret=v)` 可逐值反推 | `IS NULL` 随比较为假→不放行；行集差异仍可探测 | 泄露 |
| 乙：不可见列当 NULL | `NOT(NULL=1)`=NOT UNKNOWN=UNKNOWN→不放行 | `NULL IS NULL`=TRUE→返回全部行 | 泄露存在性与行集 |
| 丙：整查询拒绝（本实现） | 返回 `ErrInvisibleColumn`，指 `secret@[0]`，无任何行 | 返回 `ErrInvisibleColumn`，指 `secret@[0]`，无任何行 | 不泄露 |

测试：`filter.TestApplyAndProbes`、`filter.TestAnalyze` 对两个探针断言
`errors.Is(err, ErrInvisibleColumn)` 且行结果为 nil，即不会给出甲/乙的答案。

## 表 2：32 种穷举组合结论

谓词形态固定：`=`/`NOT` 只引用列 a；`AND`/`OR` 连接 a、b、c 各一次比较；
对 8 种可见性掩码逐一循环（非展开用例），无常量故不发生短路吸收。

| 形态 | 放行数 | 拒绝数 | 放行掩码（a,b,c 可见位） |
| --- | --- | --- | --- |
| `=` | 4 | 4 | a 可见：掩码 1,3,5,7 |
| `NOT` | 4 | 4 | a 可见：掩码 1,3,5,7 |
| `AND` | 1 | 7 | 仅掩码 7 |
| `OR` | 1 | 7 | 仅掩码 7 |
| 合计 | 10 | 22 | 32/32 与定义一致 |

另测 OR 例外：`OR(TRUE, secret=1)` 与 `AND(FALSE, secret=1)` 折叠为常量，
不可见引用被吸收→放行且不上报；`OR(id=7, secret=1)` 无法证明常量→拒绝。

其余实测：谓词访问计数恒等于 `Count(tree)`（`TestSingleTraversal`）；
1000 列 5 可见时 `Copies()=5 <= 20`（`TestProjectionCostAndShape`）；
裁剪行逐键断言只含可见列；报告 20 次打乱构造顺序逐字节相同
（`TestReportDeterminism`，亦含列声明顺序打乱）。
