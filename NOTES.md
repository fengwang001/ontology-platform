# Pratt 解析器推导笔记

## 三、`2 ^ 3 ^ 2` 分步表（`^`：lbp=30 rbp=30；循环条件 `lbp >= min_bp`）

| 步 | parseExpr(min_bp) | 读到 | 循环判断 | 本层子结果 |
|---|---|---|---|---|
| 1 | 0 | 前缀 NUMBER 2 | — | lhs=2 |
| 2 | 0 | 中缀 `^` | 30>=0 继续，rhs=parseExpr(30) | （待 rhs） |
| 3 | 30 | 前缀 NUMBER 3 | — | lhs=3 |
| 4 | 30 | 中缀 `^` | 30>=30 继续，rhs=parseExpr(30) | （待 rhs） |
| 5 | 30 | 前缀 NUMBER 2 | — | lhs=2 |
| 6 | 30 | EOF | 非中缀，停 | 返回 2 |
| 7 | 30 | （步 4 的 rhs=2） | EOF，停 | 3^2=9，返回 9 |
| 8 | 0 | （步 2 的 rhs=9） | EOF，停 | 2^9=512 |

结果 512 = 2^(3^2)，右结合：内层 min_bp=30 时 `30>=30` 成立，把第二个 `^` 吞进右操作数。

- **(甲)** `-` 的 rbp 误设为 10：`10-4-3` 内层 parseExpr(10) 读到第二个 `-` 时 10>=10 继续 → 10-(4-3)=**9**（应为 3，断链方向反转成右结合）。
- **(乙)** 循环条件误写 `>`：`2^3^2` 内层 30>30 不成立即停 → 外层先算 2^3=8 再遇第二个 `^` → (2^3)^2=**64**（应为 512，右结合退化成左结合）。
- **(丙)** 一元 `-` 的 min_bp 误写 0：`-2^2` 操作数层 parseExpr(0) 读到 `^` 时 30>=0 继续 → -(2^2)=**-4**（应为 (-2)^2=4）。

## 二、四条不变量落点

1. **与朴素参照一致**：`api/api.go` 内独立的硬编码优先级递归下降参照 `refEval`（显式括号化 + 复用同一 AST 求值器）与 `Eval` 对照；`api/api_test.go: TestInvariantRef`（固定表 + 200 条随机表达式）钉住。
2. **幂右结合**：`pratt/pratt.go` `infixBP[lex.Pow]={30,30}` + 循环条件 `>=`；`pratt/pratt_test.go: TestRightAssoc` 钉住。
3. **前缀/中缀 `-` 不冲突**：`parseExpr` 入口的前缀分支（操作数以 min_bp=40 解析）与循环体内的中缀分支分离；`pratt/pratt_test.go: TestPrefixInfix` 钉住。
4. **失败不留痕**：词法/括号/指数/除零/越界/空表达式全部返回哨兵错误（`lex.ErrIllegalChar`、`lex.ErrNumRange`、`pratt.ErrSyntax/ErrParen/ErrBadExp/ErrDivZero/ErrOverflow`），无任何"看似正确"的值；`api/api_test.go: TestErrors` 与 `TestRejectedThenOK` 钉住。
