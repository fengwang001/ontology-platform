# LR(1) 闭包推导：S'->S; S->CC; C->cC|d，I0={[S'->·S,$]}

| 步 | 新加入项 | 驱动项与前瞻符 |
|---|---|---|
| 1 | [S'->·S,$] | 种子 |
| 2 | [S->·CC,$] | 由步1，B=S，β=ε，b∈FIRST($)={$} |
| 3 | [C->·cC,c] | 由步2 [S->·CC,$]，B=C，b∈FIRST(C$)={c,d} |
| 4 | [C->·cC,d] | 同步2，b=d |
| 5 | [C->·d,c] | 同步2，b=c |
| 6 | [C->·d,d] | 同步2，b=d |

c、d 为终结符，· 后不再是非终结符，闭包于第 6 项到达不动点，共 6 项。

(甲) LR(0) 不带前瞻符，核心只有 4 项：S'->·S、S->·CC、C->·cC、C->·d。LR(1) 下 [C->·cC] 按前瞻符 **c 与 d 分裂为两项**（[C->·d] 同样分裂为 c/d 两项）。
(乙) 若 FIRST(C) 被错算成 {c}（漏 d），FIRST(C$) 只剩 {c}，d 前瞻符的两项都加不进来：少 [C->·cC,d] 与 [C->·d,d]（题目点名后者），总项数 **6 → 4**。
(丙) S->Ab、A->ε|a：对 [S->·Ab,$]，FIRST(b$)={b}（b 是终结符不可空），故 [A->·ε,b] 与 [A->·a,b] 前瞻符**都是 b**；若错用 FIRST(A)={a,ε} 当前瞻符，会错成 **a**（把 ε 当传播符还会错误漏进 $），正确答案 b 丢失。

## 四条不变量的代码位置与钉住测试

1. 与朴素参照一致：朴素「整集重扫到不动点」参照 `naiveClosure`（api/api.go），对照工作队列 `closureCore`（lr/lr.go）；测试 `TestNaiveReferenceRandom`（随机文法循环）钉住。
2. 闭包不动点+前瞻符有据：`Closure`（lr/lr.go）经 `closureCore` 队列展开，仅加入 b∈FirstSeq(β,a) 的项；测试 `TestIdempotenceAndJustification` 钉住（再闭包相等且每项可溯源到驱动项 FIRST(βa)）。
3. goto 正确：`Goto`（lr/lr.go）只推进 · 后恰为 X 的项为核，再整体闭包；测试 `TestGotoKernel` 钉住核项前缀与「=核的闭包」。
4. 失败不留痕：`gram.New` 校验符号/开始符号，`validateItem`（lr/lr.go）校验点位置/前瞻符，四条哨兵错误整体返回；测试 `TestSentinelErrors`（四错互不相同）与 `TestFailureLeavesNoTrace`（拒后再调正常）钉住。

非导出计数器 `closureStats.rechecked`（lr/lr.go）仅包内测试 `TestWorkQueueNoProcessTwice` 直读，m=100…10000 断言 0；不经过任何导出接口。
