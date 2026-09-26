# NOTES

`2^3^2` 分步表（循环条件 `>=`；中缀 rbp 用绑定力表；前缀 `-` 传 min_bp=40）

| min_bp | 前缀记号 | 是否继续循环（lbp 与 min_bp 比较） | 本层子结果 |
|---|---|---|---|
| 0 | NUMBER 2 | `^` lbp30>=0 成立，消费，递归 parseExpr(30) | 2（暂存） |
| 30 | NUMBER 3 | `^` lbp30>=30 成立，消费，递归 parseExpr(30) | 3（暂存） |
| 30 | NUMBER 2 | EOF 非中缀，停 | 2 |
| 30 | — | 回填 rhs=2：3^2 | 9 |
| 0 | — | 回填 rhs=9：2^9 | 512 |

- (甲) 若 `-` 的 rbp 误设为 10（=lbp）：内层 10>=10 成立、吞掉下一个 `-`，归约成 `10-(4-3)`=**9**（正确 3，等同右结合）。
- (乙) 若循环条件误写成 `>`：30>30 不成立，右操作数只取到 3，归约成 `(2^3)^2`=**64**（变成左结合，正确 512）。
- (丙) 若前缀 `-` 误传 min_bp=0：`^` lbp30>=0 被一元操作数吞进去，归约成 `-(2^2)`=**-4**（正确 `(-2)^2`=4）。

不变量落点（位置 → 钉住的测试函数）：

1. 与朴素参照一致：`pratt.(*parser).parseExpr` 的 `>=` 循环与按 rbp 表传 min_bp → `TestReferenceConsistency`（含随机 fuzz）。
2. `^` 右结合：`^` rbp=lbp=30 且循环用 `>=`，同层 `^` 被右侧递归吃掉 → `TestPowerRightAssoc`。
3. 前缀/中缀不冲突：`parsePrefix` 只收 NUMBER/`(`/前缀 `-`，中缀仅出现在循环；前缀固定传 40 → `TestUnaryVsBinary`。
4. 失败不留痕：哨兵错误 `lex.ErrIllegalChar`、`pratt.ErrParens/ErrExponent/ErrDivZero/ErrOverflow/ErrSyntax`，错误路径整体返回 err 不返值，每次调用新建 parser → `TestFailuresLeaveNoTrace`。
复杂度：非导出字段 `parser.cmp` 记录本调用比较个数，高 min_bp 提前停止；`TestParseExprEarlyStop` 经 `pratt.SelfCheck`（包内读 cmp）断言为小常数，数值不出包。
并发：无共享可变状态，每次调用新建 parser → `TestConcurrentEval`（race 干净）。
