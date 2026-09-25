# NOTES

## rekey 推导：New(2)，Put a..e，Rebalance(3)

| key | 字节值 | 旧分区 %2 | 新分区 %3 | 迁移方向 |
|---|---|---|---|---|
| a | 97 | 1 | 1 | 不动，留分区 1 |
| b | 98 | 0 | 2 | 迁移 0→2 |
| c | 99 | 1 | 0 | 迁移 1→0 |
| d | 100 | 0 | 1 | 迁移 0→1 |
| e | 101 | 1 | 2 | 迁移 1→2 |

(甲) c 是 1→0（向后搬）。若只搬 newPart>oldPart，c 被漏搬、残留在分区 1；Get("c") 按 %3 路由查分区 0，误报 not found，正确应返回 "C"。
(乙) 写新不删旧：b 同时在分区 0 残留、分区 2 出现，变成两份；分区 0 错含 b:"B"，正确分区 0 仅 c:"C"（分区 1=a,d；分区 2=b,e）。违反不变量 1。
(丙) 旧路由问分区 0：home=98%3=2≠0，必须返回 moved、movedTo=2；若直接按分区 0 报 not found，客户端误以为 b 全局不存在，实际 b 在分区 2、值为 "B"。

## 不变量在代码中的保证位置与钉住测试

1. 路由一致无副本：shard.Rebalance 逐 key 写 next[home] 且 h!=oldP 时 delete 旧项，shard.Put 只写算术 home 分区。测试 TestSingleResidence。
2. 与朴素重建一致：api_test 的 naiveRebuild 逐桶对照 Dump，测试 TestRebalanceMatchesNaive；SelfCheck 内置同一序列核验。
3. 二次路由正确：api.GetPartition 先算 home，p!=home 不查 map 直接返回 movedTo=home。测试 TestGetPartitionMoved。
4. 失败不留痕：api.New/Put/Get/GetPartition/Rebalance 的哨兵校验全部先于任何状态改动。测试 TestRejectedOpsNoTrace（ErrInvalidN / ErrPartitionOutOfRange / ErrEmptyKey 互异）。

复杂度：shard 内非导出计数器 probes，白盒测试 TestProbeCountConstant 钉死其与 m 无关（恒为 1）。
