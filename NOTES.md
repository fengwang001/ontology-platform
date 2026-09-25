# NOTES — ontology-500 LSM 状态存储

## 八步分步表（maxMem=2；@n=seq，S1=SST1 最旧，删=墓碑）

| 步 | 操作 | seq | memtable | SSTable 列表（旧→新） | Get 结果 |
|---|---|---|---|---|---|
| 1 | Put(k1,A) | 1 | k1=A@1 | — | — |
| 2 | Put(k2,B) | 2 | k1=A@1,k2=B@2 | — | — |
| 3 | Put(k1,C) | 3 | k1=C@3 | S1{k1=A@1,k2=B@2} | — |
| 4 | Del(k2) | 4 | k1=C@3,k2=删@4 | S1{…} | — |
| 5 | Put(k3,D) | 5 | k3=D@5 | S1{k1=A@1,k2=B@2}, S2{k1=C@3,k2=删@4} | — |
| 6 | Get(k1) | 5 | k3=D@5 | S1,S2 | **C**（mem 无→S2 命中，读放大 1） |
| 7 | Del(k1) | 6 | k3=D@5,k1=删@6 | S1,S2 | — |
| 8 | Get(k1) | 6 | k3=D@5,k1=删@6 | S1,S2 | **已删除**（mem 墓碑命中，读放大 0） |

- **(甲)** 正确返回 **C**；若从最旧 SSTable 往新查，先命中 S1 的 k1=A，错返 **A**。
- **(乙)** 正确返回**已删除**（mem 墓碑@6 最新）；若先查 SSTable 再查 mem，S2 命中 k1=C，错返值 **C**。
- **(丙)** Compact 只合并 S1+S2：k1 胜 C@3（值保留）；k2 胜者墓碑@4 且存在更旧值 B@2 → **墓碑保留**；k3 不在 SSTable（仍在 mem 为 D@5）。合后：Get(k1)=已删除(压于 mem@6)、Get(k2)=已删除、Get(k3)=D。若一律丢弃胜者墓碑，B@2 复活，Get(k2) 错返 **B=不存在旧值复活**。读放大最坏 **2 → 1**。

## 四条不变量：保证位置 / 钉住测试

1. **朴素重放一致**：写在 `api.go` write 中先校验再取全局 `seq`（单调）；读 mem→新 SST→旧 SST 即按 seq 取胜者。测试 `TestNaiveReplayRandom`（20 seed 随机写序列全 key 比对；不含 Compact——冻结从不丢墓碑，Compact 墓碑 GC 归不变量3）。
2. **读序正确（高 seq 永不被旧值遮蔽）**：`lsm.go` `Get` 从 `tables[len-1]` 逆序遍历，首个命中即返回。测试 `TestEightSteps` 与 `TestReadOrder`。
3. **墓碑正确、Compact 不复活**：`Del` 写 Deleted 条目占 seq；`lsm.go` `Compact` 对墓碑胜者查 `hasValue`（参与表中有更旧值才保留）。测试 `TestTombstoneRetention`。
4. **失败不留痕**：`api.go` `validateKey`/`New` 在加锁与任何状态变更之前返回哨兵错误，拒收路径不触 seq/mem/SST/readAmp。测试 `TestRejectedOpsNoTrace`。

复杂度：`lsm.go` 非导出 `probe` 记单表内定位检查数（map 定位恒 0/1），仅同包白盒测试 `TestProbeCountConstantInM` 直接读取；跨包只暴露 `ProbeBounded() bool`，无数值外泄。并发：`api.go` 单 mutex 串行化，`TestConcurrentPuts` + `go test -race` 钉住。
