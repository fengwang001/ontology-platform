# Raft 日志复制簿记 NOTES

## 八步推导（n=3，多数派=2，term=5，log=[1,1,1,1,1]，下标 1..5）

| 步骤 | m[2] | m[3] | n[2] | n[3] | commitIndex |
|---|---|---|---|---|---|
| S1 Replicate(2,true,5)  | 5 | 0 | 6 | 1 | 0 |
| S2 Replicate(3,true,5)  | 5 | 5 | 6 | 6 | 0 |
| S3 Append(5)            | 5 | 5 | 6 | 6 | 0 |
| S4 Replicate(2,true,6)  | 6 | 5 | 7 | 6 | 6 |
| S5 Replicate(3,true,6)  | 6 | 6 | 7 | 7 | 6 |
| S6 Elect(6)             | 0 | 0 | 7 | 7 | 6 |
| S7 Replicate(2,true,6)  | 6 | 0 | 7 | 7 | 6 |
| S8 Replicate(3,false,2) | 6 | 0 | 7 | 3 | 6 |

- (甲) S2 后 commitIndex=**0**（5 个条目全是旧任期 1，当前任期 5 无条目可提交）。不看任期会错提交成 **5**；S6 任期切换后，一个日志更短、不含这些"已提交"条目的节点可能当选新 leader，用它的日志覆盖其他节点 → 已提交条目丢失、状态机分歧（破坏 Leader Completeness）。
- (乙) S1 后 m[2]=**5**。错写成 `m[f]++` 则 m[2]=**1**；同样地 S2 后 m[3]=1、S4 后 m[2]=2，条目 6 凑不齐多数派，S4 正确的 commitIndex=6 被拖后成 **0**（只能等后续逐条 ++ 慢慢爬）。
- (丙) S8 后 n[3]=**3**。错写成 `n[f]--` 则 n[3]=**6**；从 6 退到正确值 3 还要 3 次拒绝 RPC（6→5→4→3），即多做 **3** 次。

## 四条不变量：保证位置 + 钉住测试

1. 与朴素重算一致：`repl.CommitIndex/advance` 只对 n 个 match 值排序取多数派阈值下标、再查该条 term；日志 term 单调不减（Append 只用当前 term、Elect 只升 term）保证阈值法与全扫描等价。测试：`repl` 包 `TestNaiveAgreement`。
2. 簿记自洽：`repl.Replicate` 先校验 `0<=r<=Len` 再赋值（ok 时 m=r、n=r+1；拒绝时 m 不变、n=r+1），必落在界内。测试：`TestBookkeepingBounds`。
3. commitIndex 单调：`advance` 仅在 candidate>commitIndex 时上调，`Elect` 不触碰 commitIndex。测试：`TestCommitIndexMonotonic`。
4. 失败不留痕：`Append/Replicate/Elect` 全部先校验（四个互不相同的哨兵错误）后改状态，校验失败直接返回。测试：`TestRejectedOpsNoStateChange`。
