# LSM 状态存储 NOTES

## 八步推导（maxMem=2；mem 满即先冻结再写入）

| # | 操作 | seq | memtable | SSTable 列表（旧→新） | Get 结果 |
|---|------|-----|----------|----------------------|----------|
| 1 | Put(k1,A) | 1 | k1=(A,1) | （空） | — |
| 2 | Put(k2,B) | 2 | k1=(A,1) k2=(B,2) | （空） | — |
| 3 | Put(k1,C) | 3 | k1=(C,3) | SST1{k1=(A,1) k2=(B,2)} | — |
| 4 | Del(k2) | 4 | k1=(C,3) k2=(†,4) | SST1{同上} | — |
| 5 | Put(k3,D) | 5 | k3=(D,5) | SST1{k1=A,k2=B} SST2{k1=(C,3) k2=(†,4)} | — |
| 6 | Get(k1) | 5 | 同上 | 同上 | C（mem 未中，SST2 命中） |
| 7 | Del(k1) | 6 | k3=(D,5) k1=(†,6) | 同上 | — |
| 8 | Get(k1) | 6 | 同上 | 同上 | 已删除（mem 墓碑命中） |

- (甲) 第 6 步正确返回 **C**。若从最旧往新查，SST1 先命中 k1=(A,1)，错返回 **A**。
- (乙) 第 8 步正确返回 **已删除**。若先查 SSTable 再查 memtable，SST2 先命中 k1=(C,3)，错返回 **C**。
- (丙) Compact(SST1+SST2)：k1 胜者 (C,3) 是值 → 保留 **k1=C**；k2 胜者 (†,4) 是墓碑且 SST1 有更旧值 B → **墓碑保留**；k3 不在参与合并的 SSTable 中（仍在 memtable），不进合并结果。若实现把胜者墓碑一律丢弃，SST1 的 (B,2) 会留在合并表里，Get(k2) 错**复活成 B**（正确应为「已删除」）。Compact 后 SSTable 数 2→1，对缺席 key 的读放大从 **2 降为 1**。

## 四条不变量：保证位置与钉住它的测试

1. 与朴素参照一致：`lsm.Store.Get` 按 mem→SST 新到旧取首命中（lsm/lsm.go），等效按 seq 重放；测试 `TestNaiveConsistency`。
2. 读序正确（seq 最大者胜）：`lsm.Store.Get` 的遍历顺序 + 写时 seq 单调分配（lsm/lsm.go）；测试 `TestNewestWins`。
3. 墓碑正确：`mem.Entry.Tomb` 与 `lsm.Compact` 的墓碑保留规则（有更旧值则留墓碑）；测试 `TestTombstoneSemantics`。
4. 失败不留痕：`api` 在触及 lsm 前先校验（api/api.go 守卫子句，哨兵错误）；测试 `TestRejectedOpsNoSideEffect`。
