# NOTES — 首值增量维护

## 八步推导（maxEvents=8）

| 步 | 操作 | 活跃事件多重集 | 首值 |
|---|---|---|---|
| 1 | Add(a,5) | {a@5} | a@5 |
| 2 | Add(b,5) | {a@5, b@5} | a@5 |
| 3 | Add(a,3) | {a@5, b@5, a@3} | a@3 |
| 4 | Add(a,3) | {a@5, b@5, a@3×2} | a@3 |
| 5 | Remove(a,3) | {a@5, b@5, a@3} | a@3 |
| 6 | Remove(a,3) | {a@5, b@5} | a@5 |
| 7 | Remove(a,5) | {b@5} | b@5 |
| 8 | Remove(b,5) | {} | 无 |

(甲) 正确 a@5（TS 并列时按 Key 升序，a<b）；比较器误写成 `>`（降序）则第 2 步错成 b@5。
(乙) 正确提升为 a@5（堆中次首）；首值缓存成单一指针、命中撤回后置空，则第 6 步错成「无」，过期后不补位。
(丙) 正确仍为 a@3（只撤掉一次，还剩一次出现）；去重实现第 4 步空转、第 5 步把 a@3 全删光，首值错成 a@5。不变量 3 要求按出现次数建多重集：重复加入必须各占一项，一次 Remove 只撤一次，否则「加 K 次撤 K 次」回不到原状态。

## 四条不变量：保证位置 / 钉住测试

1. 与朴素重扫一致：`first/first.go` 每次 Add/Remove 末尾由 `prune` 弹出计数为 0 的过期堆顶，非空时堆顶即全局最小，`First()` 纯只读返回堆顶；`TestNaiveRescanConsistency` 随机序列逐操作对拍朴素扫描。
2. 撤回正确 / 次首提升：`first/first.go` `Remove` 只减计数并立即 prune 过期堆顶，不缓存首值；`TestRemovePromotesNext` 反复撤首值核对提升链。
3. 多重集语义：`first/first.go` `counts map[evt.Event]int` 按出现次数计数、堆中每次出现压入一项；`TestMultisetRoundTrip` 加 K 次撤 K 次复原。
4. 失败不留痕：`api/api.go` 先校验（空 Key / 不存在 / 超容量）全部通过后才改状态，三个哨兵错误互异；`TestRejectedOpsAtomic` 核对被拒前后状态逐字段不变且可继续使用。
