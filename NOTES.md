# NOTES — 事件溯源重放推导与不变量落点

## 一、五步分步表（从 balance=0 全量重放，规则 max(0, balance+Delta)）

| Seq | Delta | balance |
|----:|------:|--------:|
| 1 | +10 | 10 |
| 2 | -3  | 7  |
| 3 | +7  | 14 |
| 4 | -20 | 0  |
| 5 | +2  | 2  |

快照 {Seq:3, Total:14} 与上表 seq3 行一致。正确重放只应用 seq>3：14→(seq4)→0→(seq5)→**2**。

## 二、三问

- (甲) 错把 seq3 再应用一遍，重放 [seq3,seq4,seq5]：14+7=21 → 21-20=1 → 1+2=**3**（正确 2）。
- (乙) 混入旧事件 (1,+10) 不跳过，重放 [seq1,seq4,seq5]：14+10=24 → 24-20=4 → 4+2=**6**（正确 2）。
- (丙) 乱序重放 [seq5,seq4]：14+2=16 → max(0,16-20)=**0**（正确 2）。原因：max(0,·) 下界截断是非线性的，
  先 +2 把余额垫到 16，再 -20 时只亏 16，亏空的 4 被截断吞掉；而正确顺序先 -20 已触底，+2 从 0 起算。加法可交换，截断不可交换。

## 三、四条不变量的落点

1. 重放一致：`replay.Replay` 从 snap.Total 起只应用 Seq>snap.Seq；`api.Replay(snap=零快照)` 等价全量。测试 `TestReplayConsistency`。
2. 幂等：`replay.Replay` 内 `ev.Seq <= snap.Seq` 一律 skip，重复 Seq 不重复生效。测试 `TestIdempotent`。
3. 快照边界：定位用二分 `sort.Search` 找首个 Seq>snap.Seq，从 snap.Seq+1 起应用。测试 `TestSnapshotBoundary`。
4. 失败不留痕：`es.ValidateSnapshot/ValidateEvent`、`replay` 前置校验乱序、`api.Append` 先校验后变更。测试 `TestFailureAtomic`。
