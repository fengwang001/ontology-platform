# FINDINGS

## 表 1：三种做法在两个探针下的行为（角色可见列不含 secret）

| 做法 | `NOT (secret = 1)` | `secret IS NULL` |
| --- | --- | --- |
| 甲：不可见列视为 false | 恒真 → 返回全部行（含真实 secret=1 的行），与 `secret = 1` 的空集互补，暴露隐藏列存在 | 恒假 → 空集 |
| 乙：不可见列视为 NULL（三值逻辑） | UNKNOWN → 空集 | 恒真 → 返回全部行，`IS NULL` 沦为存在性探针 |
| 丙：拒绝执行（本实现） | 拒绝：`ErrInvisibleColumn`，指名 `secret@NOT/CMP`，不返回任何行 | 拒绝：`ErrInvisibleColumn`，指名 `secret@CMP`，不返回任何行 |

本实现实测（`go test ./filter/ -run TestCheckProbes` 与 demo 第 3、4 行）：
两个探针均返回可判定错误，列名与谓词树路径随错误一并给出，无任何行泄露。

## 表 2：32 种穷举组合结论（3 列 a/b/c × 8 种可见性掩码 × 4 种谓词形态）

| 谓词形态 | 模板 | 放行组合数 | 拒绝组合数 | 放行条件 |
| --- | --- | --- | --- | --- |
| `=` | `a = 1` | 4 | 4 | a 可见（b、c 无关） |
| `NOT` | `NOT (a = 1)` | 4 | 4 | a 可见 |
| `AND` | `a=1 AND b=2 AND c=3` | 1 | 7 | a、b、c 全部可见 |
| `OR` | `a=1 OR b=2 OR c=3` | 1 | 7 | a、b、c 全部可见 |

合计 32 种：放行 10、拒绝 22，与 DESIGN.md 的定义逐一吻合
（`filter.TestExhaustive` 用循环覆盖全部 32 种，拒绝时断言被指名的列
恰为不可见列）。

## 附：OR 短路例外（常量折叠吸收，不计入上表）

| 谓词 | 折叠结果 | 结论 |
| --- | --- | --- |
| `secret=1 OR true` | `true` | 放行（secret 未被读取） |
| `secret=1 AND false` | `false` | 放行（AND 对偶情形） |
| `NOT (secret=1 OR true)` | `false` | 放行 |
| `secret=1 OR false` | `secret=1` | 拒绝，指名 secret |
| `secret=1 AND true` | `secret=1` | 拒绝，指名 secret |

## 其他实测结论

- 行裁剪：1000 列、5 列可见时拷贝 5 次（上界 4×5=20），被裁列从行中
  删除而非置零值，与零值填充行可区分。
- 谓词检查：单遍 DFS，访问节点数 == 折叠后树的节点总数。
- 报告：20 次打乱构造顺序序列化逐字节相同；同列多次出现只报一次、
  路径全部保留且排序去重。
