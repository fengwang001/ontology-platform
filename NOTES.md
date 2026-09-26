# Raft 任期与投票簿记 — 推导与不变量

## 六步推导（n=3，多数派=2；初始 term=[0,0,0]，votedFor=[-1,-1,-1]）

| 步 | 操作 | term | votedFor | Winner |
|---|---|---|---|---|
| S1 | StartElection(0) | [1,0,0] | [0,-1,-1] | 无 |
| S2 | RequestVote(0,1)→节点1 | [1,1,0] | [0,0,-1] | 0 |
| S3 | RequestVote(0,1)→节点2 | [1,1,1] | [0,0,0] | 0 |
| S4 | RequestVote(1,1)→节点2 | [1,1,1] | [0,0,0] | 0（拒绝：节点2在任期1已投0） |
| S5 | RequestVote(2,0)→节点0 | [1,1,1] | [0,0,0] | 0（拒绝：t=0 < term[0]=1） |
| S6 | StartElection(1) | [1,2,1] | [0,1,0] | 0 |

- (甲) S5：正确实现拒绝、状态不变。若实现不比较任期、见票就同意，`votedFor[0]` 会错成 **2**（正确应为 **0**）。
- (乙) S6：正确应 `term[1]=2`、`votedFor[1]=1`。若忘了自增任期，节点 1 卡在任期 1：它在任期 1 已投给节点 0（S2），同任期节点 0、2 也都已投 0，没有任何节点能在任期 1 再投给 1，永远凑不到多数派 2 票，无法获胜。
- (丙) S4：正确实现拒绝。若允许同一任期重复投票、直接覆盖，`votedFor[2]` 会错成 **1**（正确应为 **0**），违反第二节**不变量 2**（每节点每任期至多一票）。

## 四条不变量：保证位置 → 钉住它的测试

1. 与朴素重算一致：`elect.move` 增量维护每候选得票数与 winner（elect/elect.go）→ `TestWinnerMatchesNaive`
2. 每节点每任期至多一票：`term.Book.RequestVote` 的 `t==term` 分支只在未投或同候选时同意（term/term.go）→ `TestOneVotePerTerm`
3. 任期单调：`term` 只在 `StartElection` 自增或 `t>term` 时提升，拒绝路径不写状态（term/term.go）→ `TestTermMonotonic`
4. 失败不留痕：`elect` 先校验（ErrNodeIndex / ErrBadTerm / ErrSelfVote）后改状态，拒绝路径零写入（elect/elect.go）→ `TestRejectedNoStateChange`
