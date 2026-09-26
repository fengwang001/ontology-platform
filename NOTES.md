# Raft 日志复制簿记推导（n=3，多数派=2，初始 term=5，log[1..5] 任期全为 1，leader=1）

| 步 | match[2] | match[3] | next[2] | next[3] | commitIndex |
|---|---|---|---|---|---|
| S1 Replicate(2,true,5)  | 5 | 0 | 6 | 1 | 0 |
| S2 Replicate(3,true,5)  | 5 | 5 | 6 | 6 | 0 |
| S3 Append(5)（增下标6，term=5） | 5 | 5 | 6 | 6 | 0 |
| S4 Replicate(2,true,6)  | 6 | 5 | 7 | 6 | 6 |
| S5 Replicate(3,true,6)  | 6 | 6 | 7 | 7 | 6 |
| S6 Elect(6)             | 0 | 0 | 7 | 7 | 6 |
| S7 Replicate(2,true,6)  | 6 | 0 | 7 | 7 | 6 |
| S8 Replicate(3,false,2) | 6 | 0 | 7 | 3 | 6 |

(甲) S2 后正确 commit=0（无当前任期条目）；只看多数派不看任期会错提交成 5。S6 后若日志更短的节点在新任期当选，它会用自己的短日志覆盖 1..5，已提交条目被删除丢失，违反 Leader Completeness。
(乙) S1 后正确 match[2]=5；把接受写成 ++ 会错成 1；S4 时 match[2]=2，下标 6 未达多数派，commitIndex 被拖后成 0（正确为 6）。
(丙) S8 后正确 next[3]=3；把拒绝写成 nextIndex-- 会停在 6，还要多做 3 次退避 RPC（6→5→4→3）。

## 四条不变量的保证位置与钉住测试

1. 与朴素重算一致：`repl.go` 的 `CommitIndex` 取多数派阈值下标且只查那一条 term；`selfcheck.go` 的 `SelfCheck` 与朴素整表扫描 oracle 逐步对比。测试 `TestNaiveRecompute`。
2. 簿记自洽：`New`/`Elect` 给初值，`Replicate` 先校验 r∈[0,Len] 再赋 match=r、next=r+1。测试 `TestBookkeeping`。
3. commitIndex 单调：`CommitIndex` 只在 `c > commit` 时前推，`Elect` 不触碰 commit。测试 `TestNaiveRecompute`（循环内断言不回退）。
4. 失败不留痕：`Append`/`Replicate`/`Elect` 在改任何状态前返回四个互不相同的哨兵错误。测试 `TestRejectedOpsNoTrace`。

另：复杂度由 `TestCommitReadComplexity` 钉住（非导出字段 `reads` 由同包 `checkReads` 读取判定，不导出数值）；并发由 `TestConcurrentReaders` 钉住；八步真值由 `TestEightSteps` 钉住。
