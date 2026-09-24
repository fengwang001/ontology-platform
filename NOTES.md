# NOTES

## 八步表（maxEvents=8；×k 表示同一 (Key,TS) 出现 k 次）
| # | 操作后活跃多重集 | 首值 |
|---|---|---|
| 1 | {a@5} | a@5 |
| 2 | {a@5, b@5} | a@5 |
| 3 | {a@3, a@5, b@5} | a@3 |
| 4 | {a@3×2, a@5, b@5} | a@3 |
| 5 | {a@3×1, a@5, b@5} | a@3 |
| 6 | {a@5, b@5} | a@5 |
| 7 | {b@5} | b@5 |
| 8 | {} | 无 |

(甲) 第 2 步正确首值 a@5（TS 并列比 Key 字典序，a<b）；比较器误写 `>` 降序则错成 **b@5**。
(乙) 第 6 步正确新首值 a@5（次首立即提升）；把首值缓存成单一指针、命中撤回后置空则错成 **「无」**，不提升次首。
(丙) 第 5 步正确首值仍 a@3（还剩一次出现）；若集合去重（第 4 步空操作、Remove 删全部）则错成 **a@5**。故必须含重复地建多重集：K 加 K 撤才回到原状，且每次重复出现都参与首值判定。

## 四条不变量的落点
1. 与朴素重扫一致：`first.(*Set).First` 恒返回最小堆堆顶（不缓存）；由 TestFirstMatchesNaiveRescan（随机序列对拍朴素重扫）与 SelfCheck 钉住。
2. 撤回正确/次首提升：`first.(*Set).Remove` 计数减到 0 即 heap.Remove 摘除，`First` 现取现算；由 TestRemovePromotesNext 钉住。
3. 多重集语义：`cnt map[evt.Event]int` 记出现次数，Add +1、Remove -1；由 TestMultisetSemantics 钉住。
4. 失败不留痕：`api.(*Manager).Add/Remove` 全部校验（ErrEmptyKey/ErrCapacity/ErrNotFound）通过后才改状态；由 TestRejectedOpsLeaveState 钉住。
