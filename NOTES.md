# NOTES — Raft 日志复制与提交推导

## 六行分步表（条目简写 `t/i/cmd`；CI = 该步之后领导者的 commitIndex）

| 步 | 动作 | S1 | S2 | S3 | CI |
|---|---|---|---|---|---|
| 1 | t1，S1 领导：Append a；Rep(S1,S2,0)；Commit(S1) | 1/1/a | 1/1/a | （空） | 1 |
| 2 | t1：Append b；Rep(S1,S2,1)；S1 崩溃，未 Commit | 1/1/a 1/2/b | 1/1/a 1/2/b | （空） | 1（未动） |
| 3 | t2，S3 领导，S1/S2 宕：Append x | 宕 | 宕 | 2/1/x | 0 |
| 4 | t2：Append y | 宕 | 宕 | 2/1/x 2/2/y | 0 |
| 5 | t3，S1 领导，S3 宕：Rep(S1,S2,1)；Append z；Rep(S1,S2,2)；Commit(S1) | 1/1/a 1/2/b 3/3/z | 1/1/a 1/2/b 3/3/z | 宕 | 3 |
| 6 | t3：Rep(S1,S3,0) | 同上 | 同上 | 1/1/a 1/2/b 3/3/z | 3 |

## 三问

- (甲) `(2,1,x)`、`(2,2,y)` 从未被提交（仅在 S3 单副本，不足多数派 2）。正确实现第 4 步后 CI=0。若「Append 即提交」，CI 被错成 2；这两条假已提交条目在第 6 步被 Rep(S1,S3,0) 整段截断丢失，领导者完整性被破坏。
- (乙) 正确实现：S3 在 prevIndex=0 处匹配，截断到 0 后重放，最终为 `1/1/a 1/2/b 3/3/z`。若只按 index 匹配：index 1「有条目」即算匹配，`2/1/x` 被保留，S3 残留为 `2/1/x 1/2/b 3/3/z`，index 1 错成 `(2,1,"x")`（应为 `(1,1,"a")`）。
- (丙) 正确实现 CI=3：靠当前任期条目 `(3,3,"z")` 的提交，把老任期 `(1,2,"b")` 间接一并提交。若直接提交老任期多数派条目，第 5 步 Rep(S1,S2,1) 后 CI 先被错成 2；此后 S1 崩溃、S3 携更高 term 归来当选并复制其 `2/1/x 2/2/y`，「已提交」的 `(1,2,"b")` 被 `(2,2,"y")` 覆盖丢失。

## 四条不变量：代码位置与钉住它的测试

1. 日志匹配：`raft.Replicate` 仅在 `entry.Match` 前缀相同处截断重放（raft/raft.go）；`TestReplicateTruncate`、`TestScriptSixSteps`。
2. 领导者完整性：`raft.CommitIndex` 只前进且只计入 term==currentTerm 的条目，已提交前缀在任何匹配点截断下必保留（raft/raft.go）；`TestScriptSixSteps` 第 6 步 + `TestConcurrentRead`（并发调用 `SelfCheck`，其内置脚本核验已提交条目在 S3 归来后仍保留）。
3. 提交边界：`raft.CommitIndex` 的「多数派同 term 且 term==leader.Term」双条件（raft/raft.go）；`TestScriptSixSteps` 第 5 步（CI=3 而非 2）。
4. 失败不留痕：`ErrEmptyCmd`/`ErrPrevIndexRange`/`ErrPrevTermMismatch` 三条路径都在任何写入之前返回（raft/raft.go）；`TestFaultInjection`。
