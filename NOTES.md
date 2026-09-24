# EXCEPT ALL 增量物化视图 — 推导与不变量

## 一、十步分步表（New(100)；9–10 行的 cntL/cntR/out 指 b；视图只列非零行）
| # | 变更 | cntL | cntR | out | 输出日志 | 下游视图 |
|---|---|---|---|---|---|---|
| 1 | R+1 a | 0 | 1 | 0 | 无 | {} |
| 2 | L+1 a | 1 | 1 | 0 | 无 | {} |
| 3 | L+1 a | 2 | 1 | 1 | {a,+1} | {a:1} |
| 4 | L+2 a | 4 | 1 | 3 | {a,+2} | {a:3} |
| 5 | R+1 a | 4 | 2 | 2 | {a,-1} | {a:2} |
| 6 | R+3 a | 4 | 5 | 0 | {a,-2} | {} |
| 7 | L−1 a | 3 | 5 | 0 | 无 | {} |
| 8 | R−4 a | 3 | 1 | 2 | {a,+2} | {a:2} |
| 9 | L+1 b | 1 | 0 | 1 | {b,+1} | {a:2,b:1} |
| 10 | R+1 b | 1 | 1 | 0 | {b,-1} | {a:2} |

- **甲**：第 3 步 a=1，全部十步后 a=2。若做成集合语义 EXCEPT（cntL>0 且 cntR=0 才为 1）：两时刻 cntR 均非 0，a 都错成 **0、0**。
- **乙**：若不做 max(0,·)：第 6 步发 **{a,-3}**、第 7 步发 **{a,-1}**；第 7 步后下游视图 a=**-2**，违反**不变量 2（视图非负）**；十步后最终视图碰巧仍为 {a:2}，但中间前缀已非法。
- **丙**：第 1 步应「无输出」但 cntR(a) 必须记 1；若 cntL=0 时忽略 R：第 2 步错发 **{a,+1}**，第 8 步 a 变 3，最终 a 错成 **3**（应为 2）。

## 二、不变量落点
1. **前缀=批量重算**：exc.go `Operator.Apply` 的逐条循环只按受影响行算 new−old 差量（oldM/oldOther/oldOut 三读），api.go `Apply` 先全量校验再顺序提交。钉：`TestPrefixMatchesRecompute`、`TestTenStep`。
2. **视图非负、零不出现**：exc.go `Apply` 用 max(0,cntL−cntR)（`if diff<0 {diff=0}`），newOut=0 时 `delete` 视图条目。钉：`TestViewNonNegative`。
3. **变更最小**：exc.go `Apply` 仅在 newOut≠oldOut 时恰产一条 d=new−old（|d|≤|Δ|）。钉：`TestChangeMinimal`。
4. **失败不留痕**：api.go `Apply` 先做纯校验（ErrInvalidChange）；exc.go `Apply` 遇状态错误按逆序 revert 已提交条目（mset 逆 Delta、union 减 ud、视图还原 oldOut）（ErrUnderflow / ErrTooManyRows）。钉：`TestRejectedBatchLeavesNoTrace`、`TestSentinelErrorsDistinct`。

并发：api.go 用 RWMutex、View 返回拷贝，钉 `TestConcurrentViewBatchBoundaries`。触达计数：exc.go 非导出字段 `touched`，钉 `TestTouchedBounded`（包内白盒测试，不经导出接口）。
