# FINDINGS

测试结论（`go test -count=1 ./...` 与 `-race` 全绿；`go run ./cmd/demo` 12 项全 OK）。

## 表一：探针查询下三种做法 vs 本实现

场景：角色可见 `id,name`；`secret` 存在但不可见；数据中有行 `id=7, secret=1`。

| 探针查询 | 甲（不可见比较当作假） | 乙（不可见列当作 NULL） | 丙 / 本实现 |
| --- | --- | --- | --- |
| `id=7 AND NOT (secret = 1)` | 返回 id=7（含 secret=1 的行，谓词实为假） | 空集（NOT NULL → NULL） | 拒绝，`ErrInvisibleColumn`，指名 `secret@$.and[1].not`，不返回任何行 |
| `secret IS NULL` | 空集（IS NULL 当作假） | 返回全部行（恒真，且暴露列存在性） | 拒绝，`ErrInvisibleColumn`，指名 `secret@$`，不返回任何行 |

本实现实测：两个探针均触发 `*filter.RefError`（`errors.Is(err, filter.ErrInvisibleColumn)` 为真），
`Execute` 返回 `rows=nil`、`Report.Rejected=true`。甲会给出的「全部行」与乙会给出的「全部行」都未出现。

## 表二：32 种穷举组合结论汇总

列 `a,b,c`；可见性 8 种组合（每列可见/不可见）；谓词形态 4 种。
判定规则（DESIGN.md）：无常量折叠时，任一被引用列不可见即拒绝并指名。

| 谓词形态 | 模板 | 放行 | 拒绝 |
| --- | --- | --- | --- |
| `=` | `a = 1` | 4（a 可见的组合） | 4（均指名 a） |
| `NOT` | `NOT (a = 1)` | 4 | 4（均指名 a） |
| `AND` | `a=1 AND b=2 AND c=3` | 1（仅 a,b,c 全可见） | 7（指名全部不可见引用列） |
| `OR` | `a=1 OR b=2 OR c=3` | 1 | 7 |
| 合计 | | 10 | 22 |

实测与上表逐项一致（`TestExhaustive` 循环覆盖 32 种，断言放行/拒绝与指名列集合）。

## 其他实测结论

- OR 短路例外：`OR(TRUE, secret=1)`、`AND(FALSE, secret=1)` 放行；`OR(FALSE, secret=1)`、`AND(TRUE, secret=1)`、`OR(secret=1, TRUE)` 拒绝——与 DESIGN.md 定义一致。
- 单遍检查：无常量谓词访问节点数 == `predicate.Count`；被短路子树不计入。
- 行裁剪：1000 列行、5 列可见时拷贝 5 次（上界 4×5=20）；裁剪行与置零值行可区分（无 `secret` 键 vs 有键为 nil）。
- 报告确定性：同一查询 20 次打乱构造顺序，`Report.String()` 逐字节相同。
- 边界：空可见集拒绝一切引用并裁剪为空行；全可见集放行且保留全部列；空谓词（nil）与纯常量谓词放行。
