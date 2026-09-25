# NOTES

## 推导：New(2)，Put a..e，Rebalance(3)（hash = key 各字节值之和）

| key | 字节值 | 旧分区 %2 | 新分区 %3 | 迁移 |
|---|---|---|---|---|
| a | 97 | 1 | 1 | 不迁移 |
| b | 98 | 0 | 2 | 迁移 0→2 |
| c | 99 | 1 | 0 | 迁移 1→0 |
| d | 100 | 0 | 1 | 迁移 0→1 |
| e | 101 | 1 | 2 | 迁移 1→2 |

- (甲) c 的方向是 1→0。错误实现「只搬 newPart > oldPart」会漏搬 c，c 残留在分区 1；迁移后 `Get("c")` 按 %3 直查分区 0，得到 not found（正确应返回 "C"）。
- (乙) b 会同时残留在分区 0 和分区 2；分区 0 错成 `{b:B, c:C}`（c 1→0 进了分区 0），正确应为 `{c:C}`。违反不变量 1（同一 key 在非 home 分区出现副本；也连带违反 2）。
- (丙) home(b) = 98%3 = 2，正确返回 movedTo=2。若误报 not found，客户端会以为 b 全局不存在；实际它在分区 2、值为 "B"。

## 不变量的代码保证位置与钉住测试

1. 路由一致：`shard/shard.go` 的 `Put` 只写 `route.Home`，`Rebalance` 用全新分桶（写入新集即旧集整体丢弃 = 写新删旧，每 key 至多一份）；由 `TestRouteConsistency` 钉住。
2. 朴素重建一致：`shard/shard.go` 的 `Rebalance` 遍历全部旧 key 按 newN 重新算术分桶；由 `TestNaiveRebuildEquivalence` 逐 key 逐 value 比对钉住。
3. 二次路由正确：`api/api.go` 的 `GetPartition` 先算 `route.Home`，p≠home 只回 moved 提示、不查存在性；由 `TestGetPartitionRedirection`（含不存在 key 也只回 moved）钉住。
4. 失败不留痕：`api/api.go` 所有入口先做参数校验、拒绝先于任何写入，`New`/`Rebalance` 在分配前判 n；由 `TestRejectedOpsLeaveNoTrace`（拒绝前后 Dump 全等、之后仍可正常使用）钉住。
