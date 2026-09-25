# 物化视图冷启动：全量快照 + 增量切换

## 第三节推导

事件：(1,a,+10)(2,b,+20)(3,a,+1)(4,c,+30)(5,b,+5)(6,a,+2)(7,d,+40)(8,b,+3)
快照 SP=4，Table={a:11, b:20, c:30}（位点 ≤4 的累加）。冷启动逐步：

| 位点 | 事件 | applied | state（a,b,c,d 字典序） |
|---|---|---|---|
| 5 | b,+5 | 5 | a=11, b=25, c=30 |
| 6 | a,+2 | 6 | a=13, b=25, c=30 |
| 7 | d,+40 | 7 | a=13, b=25, c=30, d=40 |
| 8 | b,+3 | 8 | a=13, b=28, c=30, d=40 |

- (甲) 增量起点错写成 SP（含）：位点 4 的 c,+30 被重复累加，最终 c=60（正确 30）。
- (乙) SP 误记为「下一条要读」、Table 只覆盖 ≤3：位点 4 的 c,+30 被丢，最终 c=0（c 不存在，正确 30）。
- (丙) 增量起点错写成 SP+2：位点 5 的 b,+5 被丢，最终 b=23（正确 28）。

## 四条不变量及其保证位置与钉住测试

1. 与朴素参照一致：`sw.ApplySnapshot` 整体拷贝 Table、`sw.ApplyIncremental` 严格接续；测试 `TestColdStartMatchesNaive`（多档 SP 循环）。
2. 无缝隙无重叠：`snap.CheckContiguous` 要求 ev.Pos==applied+1，快照语义 SP 含上界；测试 `TestSwitchNoGapNoOverlap`。
3. 位点连续单调：`sw.ApplyIncremental` 先校验后推进 applied；测试 `TestContinuityMonotonic`。
4. 失败不留痕：所有校验先于任何写操作，哨兵错误 `snap.ErrGap/ErrBadSP/ErrEmptyKey`；测试 `TestRejectedNoSideEffect`。

另：增量 O(1) 由非导出计数器 `sw.lastScan` 保证，测试 `TestIncrementalO1`；并发读一致性测试 `TestConcurrentView`。
