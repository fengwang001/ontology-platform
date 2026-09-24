# FINDINGS — 测试结论记录

## 表 1：三种做法 × 两个探针

设定：角色可见 `{name, dept}`，`secret` 不可见；表中「返回」指查询正常求值并给出行。

| 探针查询 | 甲（不可见置假） | 乙（不可见置 NULL） | 丙 / 本实现 |
| --- | --- | --- | --- |
| `NOT (secret = 1)` | 返回全部行（对 secret=1 的行作了假断言） | 返回空集（NOT NULL 仍非真） | 拒绝，`ErrInvisibleColumn`，指名 `secret@$.N` |
| `secret IS NULL` | 返回空集（IS NULL 判假），且与「列不存在」可区分 → 可枚举隐藏列 | 返回全部行 → 存在性探针成立 | 拒绝，`ErrInvisibleColumn`，指名 `secret@$` |

本实现实测（`go test ./filter/` 的 TestCounterexampleProbes 与 demo 第 3、4 行）：
两个探针均返回可判定错误、不返回任何行；错误可用 `errors.Is(err,
filter.ErrInvisibleColumn)` 判定，且与 `ErrUnknownRole`、`ErrInvalidPredicate` 区分。

## 表 2：三十二种穷举组合结论（列 a/b/c × 8 种可见性掩码 × 4 种谓词形态）

判定规则（与 DESIGN.md 一致）：放行 ⟺ 谓词引用的列全部可见；拒绝时指名全部
不可见引用列。本组无常量支，折叠不改变结论。

| 形态 | 模板 | 引用列 | 放行数 | 拒绝数 |
| --- | --- | --- | --- | --- |
| `=` | `a = 1` | a | 4 | 4 |
| `NOT` | `NOT (a = 1)` | a | 4 | 4 |
| `AND` | `a=1 AND b=2 AND c=3` | a,b,c | 1 | 7 |
| `OR` | `a=1 OR b=2 OR c=3` | a,b,c | 1 | 7 |
| 合计 | | | 10 | 22 |

实测（`go test -v ./filter/ -run TestExhaustive`）：`10 allowed, 22 rejected`，
32/32 与规则一致；每个拒绝用例指名的列集合恰等于该掩码下不可见的引用列。

## 其他实测结论

- 一遍遍历：访问节点数 == 折叠后谓词树节点总数（TestTraversal）。
- 行裁剪：1000 列、5 列可见时拷贝 5 次 ≤ 4×5 上界（TestPruneCostBound）。
- 裁剪行按键排序逐字节等于 `dept=eng,name=ada`，不含 `secret` 键，
  与置零值行（含 `secret=""` 键）可区分（TestPruneRow）。
- 审计报告：同一逻辑报告打乱构造顺序 20 次，输出逐字节相同；
  被裁列字典序、同列多路径合并为一条（report 包测试）。
