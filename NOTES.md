# NOTES

## 六步推导（T1=线程1，A=节点10，B=节点20）

| 步 | 操作 | hazard(线程→节点) | 退休列表 | 返回值 |
|---|---|---|---|---|
| 1 | Protect(1,10) | {1→10} | [] | — |
| 2 | Retire(10) | {1→10} | [10] | — |
| 3 | Retire(20) | {1→10} | [10,20] | — |
| 4 | Reclaim | {1→10} | [10] | 释放[20] |
| 5 | Unprotect(1) | {} | [10] | — |
| 6 | Reclaim | {} | [] | 释放[10] |

(甲) 先解引用后发布：解引用与 Protect 之间节点可被另一线程 Retire 并经 Reclaim 回收，随后对该指针的访问即 use-after-free。先 Protect 发布、后解引用时，回收裁决在 hazard 临界区内必见该槽而跳过此节点，访问期间不可能被释放。
(乙) 第 4 步只释放 20、留下 10（10 仍被 T1 的危险指针指向）。若不看 hazard 把退休列表全放，会误放 10，而 T1 正持有并将继续解引用它 → UAF/内存崩溃。
(丙) 发布后不重读：10 已被摘除、头已变为 20，继续解引用 10 读到的是逻辑上已脱表的节点。正确做法是发布危险指针后重读头：若头≠10 则 Unprotect 并重试，这保证被解引用的节点在发布时刻仍在表中。

## 四条不变量：保证位置与钉住的测试

1. 朴素一致：retire.go 的 Reclaim 对退休表逐节点调 `hazard.Table.Unprotected` 判集合，与单 mutex 的朴素参照判定相同；TestNaiveEquivalence。
2. 安全：Unprotected 在 hazard 单一临界区内一次性裁决，Reclaim 只释放其返回的节点，故返回者此刻必无任何槽指向；TestReclaimSafety、TestConcurrentReclaimSafety。
3. 最终回收：被保护节点留在退休 order 中，每次 Reclaim 重新逐节点裁决，一旦无人指向即释放；TestEventualReclaim。
4. 失败不留痕：api 先校验 nodeID>0；Set 的“已占用”判定与 Retire 的“已退休”判定均在锁内、任何写入之前拒绝；TestErrorsLeaveNoTrace。
