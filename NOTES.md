# NOTES

## 1. 推导：六步分步表（分组初始为空）

| 步 | 变更 | COUNT | SUM | MIN | MAX |
|---|---|---|---|---|---|
| 1 | Insert 5 | 1 | 5 | 5 | 5 |
| 2 | Insert 2 | 2 | 7 | 2 | 5 |
| 3 | Insert 9 | 3 | 16 | 2 | 9 |
| 4 | Retract 9 | 2 | 7 | 2 | 5 |
| 5 | Insert 2 | 3 | 9 | 2 | 5 |
| 6 | Retract 2 | 2 | 7 | 2 | 5 |

(甲) COUNT、SUM 只需「当前值 + 本条增量」：+1/−1、+v/−v 即可。MIN/MAX 做不到——撤掉当前极值后，标量里没有「次小/次大值」的任何信息。判据：聚合对多重集的并入操作必须**可逆**，f(X∪{v}) 与 f(X\{v}) 都能只由 f(X)、v 算出（单值贡献构成群，有逆元）；MIN/MAX 幂等、无逆元，不满足。

(乙) 第 4 步朴素标量 MAX（只存 9）撤回后只能给 **0（或「无」）**，正确值是 **5**。朴素补救「撤回值等于当前最大值就清空」：第 4 步清空（错，应为 5）→ 第 5 步 Insert 2 后把 MAX 记成 2（错）→ 第 6 步 Retract 2 又清空（错）；**失效始于第 4 步，第 5、6 步持续**。重复值是它的死穴：它分不清撤回的是不是最后一份（Insert 5、Insert 5、Retract 5 会被清空，正确 MAX 仍是 5；第 6 步撤回的 2 也仍有一份存活）。最小附加状态：每分组一棵按值有序、结点带重复份数 cnt 的 treap。插入/删除/取极值都只走一条根到叶路径 O(log m)；它保存了完整次序，撤回极值后左/右链下一结点即新极值，cnt>1 兜住重复值——信息恰好足够。

## 2. 四条不变量：保证位置 / 钉住的测试

1. 逐步一致：`agg/agg.go` 的 `Group.Apply` 经 `api.Feed` 每条变更一次提交；测试 `TestFeedMatchesRecompute`（随机流含负数/重复/撤回，逐事件对拍全量重算）。
2. 撤回是插入的逆：`agg` 四聚合 Apply 的 ± 对称（treap 在 cnt>1 时只减份数）；测试 `TestRetractIsInverse`。
3. 空分组语义：`agg.Min/Max.Value` 返回 `(v,ok)`，`api.Feed` 成功后剪空分组；测试 `TestEmptyGroupSemantics`。
4. 失败不留痕：`agg.Group.Apply` 先全部校验后变更，`api.Feed` 预检 + 反向回滚；测试 `TestRejectedEventsLeaveNoTrace`（三类哨兵互不相同，拒绝前后快照全等）。
