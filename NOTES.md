# AST 常量折叠 — 推导与不变量

表达式 `x*1+y*0+3*4`，AST = `((x*1)+(y*0))+(3*4)`，自底向上单趟：

| 步 | 折叠的子表达式 | 规则 | 折叠后 |
|---|---|---|---|
| 1 | `x*1` | 5（`e*1`，x 不含 `/`，纯） | `x` |
| 2 | `y*0` | 5（`e*0`，y 纯） | `0` |
| 3 | `(x*1)+(y*0)` → `x+0` | 5（`e+0`） | `x` |
| 4 | `3*4` | 4（两个 int 字面量） | 12 |
| 5 | 外层 `+`：`x+12` | 无适用规则（非常量、非恒等），保持 | `x+12` |
| 6 | 整棵树最终结果 | — | `x+12` |

(甲) `1/0` 保持原样 `(1/0)`；若照常折叠会在折叠期求 `1/0`，整数除零直接 panic，折叠器崩溃，运行时错误被提前暴露/丢失。
(乙) `false && (1/0>0)` → `false`（规则 6，右侧完全不进入折叠）；若先折右侧会求 `1/0` 而 panic（或错误上抛），整棵折叠失败，得不到 `false`。
(丙) `(1/0)*0` 保持原样；无条件 `e*0→0` 会错成 `0`：原式对任意代入都运行时除零，折后却得 `0`，掩盖运行时错误，违反不变量 1/3。

不变量（代码保证位置 / 钉住的测试）：

1. 语义一致：fold.go `foldBin` 严格只按规则 4–6 改写；api.go `eval/ev/evBin` 朴素参照求值；由 api_test.go `TestSemanticEquivalence`（随机 AST 多组代入）与 `TestFoldCases` 钉住。
2. 短路不越界：fold.go `foldLogic` 在折叠右子之前依左字面量早返回（右子可不进入）；由 api_test.go `TestShortCircuit`（右子含未知 op / nil 也不被访问）钉住。
3. 除零保留：fold.go `litOp["/"]` 的 `b==0→ok=false` 例外 + 自底向上 danger 标志（`dl/dr/bad`）守卫恒等消去；由 api_test.go `TestDivZero` 钉住。
4. 失败不留痕：fold.go 入口 nil、未知 op、类型不一致三处哨兵错误，每次 `Fold` 新建无状态 folder；由 api_test.go `TestSentinels`、`TestSelfCheck` 钉住。

附：`folder.revisits` 为非导出计数器（fold.go），仅白盒 fold_test.go `TestSinglePass` 直接读取，断言大 m 下重复访问为 0。
