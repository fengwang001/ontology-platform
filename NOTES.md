# 增量物化视图：推导笔记

六步流（Insert 5 / 2 / 9，Retract 9，Insert 2，Retract 2）：

| 步 | 操作 | 存活值 | COUNT | SUM | MIN | MAX |
|---|---|---|---|---|---|---|
| 1 | Insert 5 | {5} | 1 | 5 | 5 | 5 |
| 2 | Insert 2 | {5,2} | 2 | 7 | 2 | 5 |
| 3 | Insert 9 | {5,2,9} | 3 | 16 | 2 | 9 |
| 4 | Retract 9 | {5,2} | 2 | 7 | 2 | 5 |
| 5 | Insert 2 | {5,2,2} | 3 | 9 | 2 | 5 |
| 6 | Retract 2 | {5,2} | 2 | 7 | 2 | 5 |

(甲) COUNT、SUM 只需一个标量加本条增量：±1、±v，对应可结合且有逆的运算 +。判据：聚合 f 能只靠「当前值+增量」维护，当且仅当 f(X∪{v}) 与 f(X\\{v}) 都能由 f(X) 和 v 唯一确定（即可结合、对 v 可消去/有逆）。MIN/MAX 不满足：撤回当前极值后，标量里没有次小/次大值，也不知道该极值还活着几份——缺的是「其余存活值的有序多重集」。
(乙) 朴素单标量 MAX 在第 4 步撤回 9 后只能报「无」（或被冒充的 0），正确值是 5。「撤回值若等于当前最大值就清空」：第 5 步 Insert 2 后 MAX 错成 2（真值 5），第 6 步 Retract 2 又清空（真值仍 5）；不带频次的实现还会在第 6 步把两个 2 一并删光，MIN 错成 5（另一份 2 仍活，真 MIN=2）。最小附加状态：存活值的有序多重集（treap，节点存频次）。频次解决重复值，最左/最右节点即极值，极值频次归零时有后继/前驱立得，故充分。

## 四条不变量的落实位置与钉住测试

1. 逐步一致：`agg.(*Group).Apply` 同步更新 count/sum/treap，`Value()` 即四元组；测试 TestSixStep、TestRandomStreamsMatchRecompute。
2. 撤回是插入的逆：treap insert/remove（含 freq>1 频次分支）严格互逆；测试 TestInsertRetractInverse。
3. 空分组语义：`agg.Value()` 在 root==nil 时 HasMin/HasMax=false，`api.Snapshot` 对未知/已清空分组同；0 值用 HasMin=true 区分；测试 TestEmptyGroupSemantics。
4. 失败不留痕：`agg.Apply` 全部预检（合法 Op、撤回存在性、SUM 双向溢出）通过后才改状态；`api.Feed` 记 journal，失败按逆序回滚整批并恢复分组表；测试 TestRejectedBatchAtomic、TestSentinelErrorsDistinct。

复杂度：非导出 `Group.accessed` 记录最近一次 Apply 访问的已存值个数；TestAccessedNotLinear 断言 ≤6·log2(m)。
并发：`sync.RWMutex`，Snapshot 走 RLock；TestConcurrentSnapshot（-race）。
