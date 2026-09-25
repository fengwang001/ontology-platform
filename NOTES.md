# NOTES

## 八步推导（A、B 为前两步 Root 返回的对象）

| # | 操作 | rc[A] | rc[B] | 本步释放 |
|---|---|---|---|---|
| 1 | Root()（返回 A） | 1 | - | 无 |
| 2 | Root()（返回 B） | 1 | 1 | 无 |
| 3 | Point(A,B) | 1 | 2 | 无 |
| 4 | Point(B,A) | 2 | 2 | 无 |
| 5 | Unroot(A) | 1 | 2 | 无 |
| 6 | Unroot(B) | 1 | 1 | 无 |
| 7 | Collect()：tmp[A]=tmp[B]=1-1=0，无救援根，清扫 | 0 | 0 | A、B |
| 8 | 观察：堆空 | — | — | 无 |

(甲) 纯引用计数：两个 rc 恒为 1，第 7 步谁也释放不了；无根环 A↔B 永久泄漏，只有试删能回收。
(乙) 根在 X、X↔Y：rc[X]=2（根+Y→X），rc[Y]=1（X→Y）；试删后 tmp[X]=1、tmp[Y]=0。不救援则 Y 被误 free，X 仍指向 Y → 悬垂指针，之后 Unroot(X) 级联会改写已释放记录。
(丙) A 自引用、无根：rc[A]=1 全来自自环；tmp[A]=1-1=0，正确 Collect 应释放 A。若「不减自引用」则 tmp[A]=1，A 被误判为被根直接引用而获救，自环永久泄漏。

## 四条不变量：保证位置 / 钉住的测试

1. 引用计数守恒：`refc.go` 的 Root/Roots、`Repoint`（先增 to 再 dec 旧 child）、`DropRoot` 与 `DecRelease` 成对增减 RC/Roots；`trial.Collect` 清扫时对「垃圾→存活」边调 `DecRelease`；`TestInvariantConservation`。
2. 与朴素参照一致：`trial.go` `Collect` 的 tmp 减法、tmp>0 救援并沿 child 闭包标记、未标记者清扫；`TestInvariantReachability`。
3. 无悬垂引用：`refc.go` `Repoint` 先增 to、改指针、再 dec 旧 child；`DecRelease` 释放前清空 child，`Collect` 只扫走整团垃圾；`TestInvariantNoDangling`。
4. 失败不留痕：`trial.go` Point/Unroot 所有变更前完成校验，`api.go` Root 超上限先预检再分配；`TestInvariantAtomicity`。

其余测试：`TestEightStepCycle`（八步）、`TestSentinelsDistinct`（三类互异哨兵错误）、`trial.TestPointChecksBounded`（Point 检查条数白盒断言）、`TestConcurrentGraph`（并发无泄漏）、`TestSelfCheck`（自检方法）。
