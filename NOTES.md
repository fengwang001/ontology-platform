# NOTES

推导：maxSlack=10，事件顺序 10,10,5,12,11,13。

| 步 | Seq | MaxSeen | 判定 | OutOfOrder | MaxLateness |
|---|---|---|---|---|---|
| 1 | 10 | 10 | 有序(首事件) | 0 | 0 |
| 2 | 10 | 10 | 相等(有序) | 0 | 0 |
| 3 | 5  | 10 | 乱序 late=5 | 1 | 5 |
| 4 | 12 | 12 | 有序 | 1 | 5 |
| 5 | 11 | 12 | 乱序 late=1 | 2 | 5 |
| 6 | 13 | 13 | 有序 | 2 | 5 |

(甲) 相等不算乱序，第 2 步后 OutOfOrder=0；若实现用 `Seq <= MaxSeen` 判乱序（把相等也算），会错成 1。
(乙) MaxLateness 取历史最大，第 5 步后仍为 5；若只记“最近一次乱序迟到量”，会错成 1。
(丙) 纯观测不丢弃，最终 OutOfOrder=2、MaxLateness=5；若见 `Seq<MaxSeen` 即丢且不进统计，两者会分别错成 0 和 0（本例 late=5/1 均 ≤10，ExceedsSlack 不置位）。

## 不变量落点与钉住测试

1. 与批量重算一致：`obs/obs.go` Feed() 用标量 maxSeen 加两个计数器；测试 `TestFeedMatchesBatchRecompute`。
2. 迟到量历史最大：`obs/obs.go` Feed() Late 分支 `if late > o.maxLate`；测试 `TestMaxLatenessHistorical`。
3. 纯观测不丢弃：`obs/obs.go` Feed() Late 分支只计数，超 slack 仅置 exceedsSlack；测试 `TestLateEventsNeverDropped`。
4. 失败不留痕：`obs/obs.go` Feed() 先判 frozen/非法 seq 再改状态，Freeze() 幂等；`api/api.go` New() 先判负 slack；测试 `TestRejectedOpsLeaveNoTrace`。

O(1) 计数器 lastChecks：`obs/obs.go` Feed() 每次成功仅置 1，不随历史增长；白盒测试 `obs/obs_test.go` 的 `TestCheckCountBounded`。
