# NOTES

## 推导（maxSlack=10，事件 10,10,5,12,11,13）

| 步 | Seq | 后MaxSeen | 判定 | OutOfOrder | MaxLateness |
|---|---|---|---|---|---|
| 1 | 10 | 10 | 有序 | 0 | 0 |
| 2 | 10 | 10 | 相等（有序） | 0 | 0 |
| 3 | 5 | 10 | 乱序 late=10-5=5 | 1 | 5 |
| 4 | 12 | 12 | 有序 | 1 | 5 |
| 5 | 11 | 12 | 乱序 late=12-11=1 | 2 | 5 |
| 6 | 13 | 13 | 有序 | 2 | 5 |

(甲) 相等不算乱序，第 2 步后 OutOfOrder 仍为 0；若把 `Seq<=MaxSeen` 都判乱序，会错成 1。
(乙) MaxLateness 取历史最大，第 5 步后仍为 5；若只记最近一次迟到量，会错成 1。
(丙) 若乱序事件（5、11）被丢弃不进统计：OutOfOrder 错成 0、MaxLateness 错成 0（正确为 2 和 5）。

## 四条不变量：保证位置 / 钉住测试

1. 与批量重算一致：obs/obs.go 的 Feed 只拿事件与标量 maxSeen 比较一次后推进；api/api_test.go 的 TestBatchEquivalence 用表驱动序列+随机排列逐项对比朴素重算。
2. 迟到量历史最大：obs/obs.go Feed 的 Late 分支 `if r.Lateness > o.maxLate`；obs/obs_test.go 的 TestFeedSteps 钉第 3 步=5、第 5 步后仍=5。
3. 纯观测不丢弃：obs/obs.go Feed 的 Late 分支无条件 `o.outOfOrder++`、更新迟到量、仅置 exceeds 标志，无任何丢弃路径；api/api_test.go 的 TestNoDropKeepsCount 钉 OOO=2、MaxLateness=5。
4. 失败不留痕：obs/obs.go Feed 先判 frozen、再经 seq.Classify 判非法序号，全部通过后才写字段；obs.New 先判负 slack；api/api_test.go 的 TestRejectedOpsLeaveNoTrace 比对拒绝前后全部统计量。
