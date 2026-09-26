# LR(1) 闭包推导与不变量对照

## 闭包分步表：I0 = { [S'->·S, $] }，文法 S'->S, S->CC, C->cC|d

| # | 新加入的项 | 驱动项 | 前瞻符来源 |
|---|---|---|---|
| 1 | [S->·CC, $]  | [S'->·S, $] | FIRST(ε$) = {$} |
| 2 | [C->·cC, c]  | [S->·CC, $] | FIRST(C$) ∋ c |
| 3 | [C->·cC, d]  | [S->·CC, $] | FIRST(C$) ∋ d |
| 4 | [C->·d, c]   | [S->·CC, $] | FIRST(C$) ∋ c |
| 5 | [C->·d, d]   | [S->·CC, $] | FIRST(C$) ∋ d |

新项的 · 后都是终结符 c/d，不再展开，闭包完成，共 6 项（含种子项）。

## 三问

- (甲) LR(0) 闭包只按核心展开，共 **4 个核心**：[S'->·S] [S->·CC] [C->·cC] [C->·d]；LR(1) 下 [C->·cC] 分裂为前瞻符 **c** 与 **d** 两个项。
- (乙) FIRST(C) 错算成 {c} 时 FIRST(C$)={c}，d 不再成为前瞻符：少 **[C->·cC, d] 与 [C->·d, d] 两项**，总项数 **6 → 4**。
- (丙) FIRST(b$)={b}，故两项前瞻符都是 **b**：[A->·, b]、[A->·a, b]；若错用 FIRST(A)={a,ε} 当前瞻符，会错成前瞻符 **a**（[A->·, a]、[A->·a, a]），把 b 丢掉。

## 四条不变量对照

1. 与朴素参照一致：`api.naiveClosure` 每轮全量重扫到不动点作参照；`SelfCheck` 不变量 1 + `TestSelfCheck`。
2. 闭包幂等且前瞻符有据：`lr.Closure` 工作队列到不动点；`SelfCheck` 不变量 2 + `TestSelfCheck`。
3. goto 正确：`lr.Goto` 只推进 · 后为 X 的项再闭包；`SelfCheck` 不变量 3 + `TestGoto`。
4. 失败不留痕：`api.ClosureOf/GotoOf` 先 `lr.Validate` 再计算，出错返回 nil；`TestClosureOfErrors`、`TestFailureLeavesNoTrace`。
