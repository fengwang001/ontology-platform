# 增量物化视图 — 推导与不变量落点

## 1. 六步变更分步对照（分组 g，初始为空）

| 步 | 变更 | 存活值 | COUNT | SUM | MIN | MAX |
|---|---|---|---|---|---|---|
| 1 | Insert 5 | {5} | 1 | 5 | 5 | 5 |
| 2 | Insert 2 | {5,2} | 2 | 7 | 2 | 5 |
| 3 | Insert 9 | {5,2,9} | 3 | 16 | 2 | 9 |
| 4 | Retract 9 | {5,2} | 2 | 7 | 2 | 5 |
| 5 | Insert 2 | {5,2,2} | 3 | 9 | 2 | 5 |
| 6 | Retract 2 | {5,2} | 2 | 7 | 2 | 5 |

(甲) COUNT、SUM 只需「当前值 + 本条增量」：±1、±v，撤回走逆运算。MIN/MAX 做不到——缺的是「当前极值被撤回后，次小/次大的存活值」，标量里没有它。判据：聚合能写成对多重集的逐元素可逆二元运算 f(T)=⊕ h(x)（结合且每条贡献可独立撤销）才可标量维护；min/max 在删除下不可逆，也没有可信幺元（0 会冒充答案）。

(乙) 第 4 步纯标量 MAX 无法作答：朴素实现给 0（或「等于最大值就清空」给「无」），正确值是 5。清空补救在第 5 步 Insert 2 后把 MAX 重建为 2（应 5），第 6 步 Retract 2 又清空为「无」（应 5）——重复值让错误贯穿第 5、6 步。最小附加状态：值→存活份数的有序多重集（treap，键存值、节点存份数），撤回只减份数、归零删键；它恰好保存全部存活值（含重复），次值永在树中，最左/最右链即 MIN/MAX，单次 Apply 期望 O(log m)。

## 2. 四条不变量：在何处保证、被谁钉住

1. 逐步一致：`agg.(*Group).Apply`（cnt/sum 标量更新，mn/mx 走 treap 多重集）— 钉住：`TestStepwiseEqualsFullRecompute`；六步表另由 `TestSixStepTable` 钉。
2. 撤回是插入的逆：`agg.(*Group).Apply` 先预检存在性再做对称增/撤 — 钉住：`TestRetractIsInverseOfInsert`。
3. 空分组报不存在：`agg.(*Group).Min`/`Max` 返回 `(v,ok)`，ok=false 即「无」；空组在 `api.(*View).Feed` 末由 `purge` 删除 — 钉住：`TestEmptyGroupReportsAbsent`。
4. 失败不留痕：`agg.(*Group).Apply` 全部校验先于任何修改；`api.(*View).Feed` 逐事件应用、失败按逆序回放逆事件并删除本次新建分组 — 钉住：`TestRejectedEventsLeaveNoTrace`。

另：O(log m) 访问计数 `Group.touched`（agg/agg.go，非导出）→ `TestApplyVisitsLogM`；并发只读快照 → `TestConcurrentSnapshotsConsistent`；自检 → `TestSelfCheck`。
