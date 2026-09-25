# seqlock NOTES

12 事件表（n=2，初值 [0,0]；A=读者A，C=读者C；“-”=无读者动作）

| # | seq | 数组 | 读者结论 |
|---|---|---|---|
| 1 | 1 | [0,0] | W1 进入临界区 |
| 2 | 1 | [1,0] | - |
| 3 | 1 | [1,1] | - |
| 4 | 2 | [1,1] | W1 离开 |
| 5 | 2 | [1,1] | A：s1=2 偶，读下标0=1，正在读 |
| 6 | 3 | [1,1] | W2 进入 |
| 7 | 3 | [2,1] | - |
| 8 | 3 | [2,1] | C：s1=3 奇 → 立即重试（不读数组） |
| 9 | 3 | [2,2] | - |
| 10 | 4 | [2,2] | W2 离开 |
| 11 | 4 | [2,2] | A：读下标1=2，s2=4≠2 → 重试（途中撕裂 [1,2]） |
| 12 | 4 | [2,2] | A：s1=4 读 [2,2]，s2=4 → 成功返回 [2,2] |

- (甲) 漏掉「奇数即重试」：C 会复制出 [2,1] 且 s2=3=s1，错误返回 **[2,1]，重试 0 次**；差异落在**下标 1**（正确实现此刻重试，最终得 [2,2]）。
- (乙) 判据写成 s2<s1：4<2 为假，A 第一次尝试即被接受，错误返回撕裂值 **[1,2]，重试 0 次**。
- (丙) 删掉进入时 seq++：写下标0后 seq 仍 **0（偶）**，读者 s1=0 读 [1,0]、s2=0，错误成功返回 **[1,0]，重试 0 次**；正确实现此刻 seq=1 奇，读者立即重试、不读数组。

不变量落点（代码位置 → 钉住的测试）：

1. 终值一致：`seq/seq.go` Snapshot 的「s1→奇偶判据→原子拷贝→s2→不等重试」 → TestTerminals、TestConcurrentNoTear
2. 版本单调：`seq/seq.go` Begin/Commit 各 seq+1（一次 Update 精确 +2），Snapshot 只读不写 → TestVersionMonotonic
3. 读不阻塞写：Snapshot 不获取 `sw/sw.go` 的写者互斥锁，仅 Update 走 Lock → TestReadDoesNotBlockWriter、TestConcurrentNoTear（`go test -race`）
4. 失败不留痕：`sw/sw.go` Update 对 nil/重入在 Begin 之前判定返回；panic 走 recover+Rollback 把 seq 补回偶数 → TestRejectedOpsLeaveNoTrace、TestPanicRecovery
