# 变更流偏移归档与保留 — 推导与不变量

## 八步推导（p=0，retention=10，驱逐条件 ts < now-10）

| # | 操作 | C | cp | 本步后归档 (off,ts) | 此刻 Restart |
|---|------|---|----|--------------------|--------------|
| 1 | Commit(100,ts=0) | 100 | - | [(100,0)] | 100 |
| 2 | Checkpoint | 100 | 100 | [(100,0)] | 100 |
| 3 | Commit(110,ts=10) | 110 | 100 | [(100,0),(110,10)] | 110 |
| 4 | Commit(120,ts=15) | 120 | 100 | [(100,0),(110,10),(120,15)] | 120 |
| 5 | Evict(now=20)，逐 ts<10 | 120 | 100 | [(110,10),(120,15)] | 120 |
| 6 | Commit(130,ts=30) | 130 | 100 | [(110,10),(120,15),(130,30)] | 130 |
| 7 | Evict(now=40)，逐 ts<30 | 130 | 100 | [(130,30)] | 130 |
| 8 | Evict(now=45)，逐 ts<35 | 130 | 100 | [] | 100 |

- (甲) 第 7 步后归档幸存 `(130,30)`，正确恢复 **130**；若 Restart 只读 cp 忽略归档，错成 **100**（丢失已提交但未检查点的 110~130）。
- (乙) 第 8 步后归档清空，正确恢复 **cp=100**；若 Restart 只读归档忽略 cp，得到 **-inf（无位点可恢复）**。
- (丙) `cp=100`、归档 `[(110,10)]`、`Evict(now=20)`：阈值 `now-retention=10`，严格小于保留 `(110,10)`，恢复 **110**；错写成 `ts <= now-retention` 会清掉它，Restart 退到 **cp=100**。

## 四条不变量：保障位置与钉住测试

1. **与批量重算一致**：`off/off.go` 的 `Partition.Recover` 本身就是 `max(cp, arc.Max())`，`api.Restart` 逐分区调用它，无第二份逻辑。测试 `TestRestartMatchesBatch`（随机操作序列对照独立镜像模型）、`TestEightStepTrace`。
2. **检查点永不驱逐**：cp 存在 `off.Partition` 独立字段，`arc.Evict` 只动归档切片，够不到 cp；`Recover` 恒取 max 含 cp。测试 `TestEightStepTrace`（第 8 步归档清空后 rec=cp=100）、`TestRestartMatchesBatch`（镜像模型中 cp 永不驱逐，逐步比对）。
3. **恢复单调**：归档只追加不修改、off/ts 单调由 `Partition.Commit` 校验、`Recover` 取 max；只要最新未检查点归档幸存，max 不减。测试 `TestRecoverMonotonic`。
4. **失败不留痕**：`api.New`、`off.Partition.Commit/Checkpoint/Evict` 全部先校验后写状态，校验失败直接返回哨兵错误，无任何赋值发生。测试 `TestRejectLeavesNoTrace`（含四哨兵互不相同校验、被拒后状态比对与继续使用）。
