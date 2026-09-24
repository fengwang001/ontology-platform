# NOTES

六行表：条目简写 `(t,i,cmd)`，`-` 为空日志，CI=CommitIndex。
| 步 | S1 | S2 | S3 | CI |
|---|---|---|---|---|
| 1 Append a; R(S1,S2,0); CI | a | a | - | 1 |
| 2 Append b; R(S1,S2,1)，S1 崩，未查 CI | a,b | a,b | - | 1（未再查） |
| 3 [term2 领导 S3] Append x | a,b | a,b | x | 未查（查则 0） |
| 4 Append y | a,b | a,b | x,y | 未查（查则 0） |
| 5 [term3 领导 S1] R(S1,S2,1); Append z; R(S1,S2,2); CI | a,b,z | a,b,z | x,y | 3 |
| 6 R(S1,S3,0)，S3 携 term2 日志归来 | a,b,z | a,b,z | a,b,z | 3 |

（甲）x、y 仅在 S3 一票，term2 从无多数派，**未提交**；正确实现第 4 步 CI=0（未调用，调用也是 0）。若「Append 即提交」，第 4 步后 CI 被错成 2；第 6 步 prevIndex=0 整体截断 S3，x、y 被删除——「已提交」条目丢失。
（乙）prevIndex=0 是空前缀，必然匹配：S3 先截断到 0 再整体追加，最终 `[(1,1,a),(1,2,b),(3,3,z)]`。若只按 index 匹配、忽略 term，会残留 `[(2,1,x),(1,2,b),(3,3,z)]`，index 1 错成 `(2,1,x)`，日志匹配性质被破坏。
（丙）正确 CI=3：当前任期的 `(3,3,z)` 在 S1、S2 过半，提交它即间接提交前缀里的 `(1,2,b)`。若直接提交老任期多数派条目，第 5 步 CI 先被错成 2（b 仅 term1 过半）；S1 再崩、S3 携更高 term 归来后，第 6 步 `(1,2,b)` 被截断覆盖，「已提交」条目丢失。

不变量（代码保证位置 / 钉住的测试函数）：
1. 日志匹配：`entry.Log.PrefixMatch` 比较 prevIndex 处 term，`raft.(*Cluster).Replicate` 仅在匹配后截断+整段追加 / `TestLogMatching`
2. 领导者完整性：提交只认当前任期多数派（`raft.(*Cluster).CommitIndex`），截断只发生在不匹配的 follower 且不会越过已匹配前缀 / `TestLeaderCompleteness`
3. 提交边界：`raft.(*Cluster).CommitIndex` 逐 index 统计同 term 副本数 ≥2 且 `term==leader.term`，老任期条目只跳过不停扫 / `TestCommitIndexQuorum`
4. 失败不留痕：空命令、prevIndex 越界、prevTerm 不等三类拒绝均在任何修改前返回互异哨兵错误 / `TestFailureLeavesNoTrace`

增量性：`CommitIndex` 自上次 commitIndex 向后扫，新增 1 条时检查数恒为 1，由 raft 包白盒 `TestIncrementalCommitCounter` 钉住；并发只读由 api 包 `TestConcurrentReaders`，自检由 api 包 `TestSelfCheck` 钉住。
