# NOTES：物化视图冷启动 全量快照 + 增量切换

## 一、第三节推导：SP=4，Table={a:11,b:20,c:30}，逐步增量

| 事件 | applied | state（a,b,c,d 字典序） |
|---|---|---|
| (5,b,+5) | 5 | a=11, b=25, c=30 |
| (6,a,+2) | 6 | a=13, b=25, c=30 |
| (7,d,+40) | 7 | a=13, b=25, c=30, d=40 |
| (8,b,+3) | 8 | a=13, b=28, c=30, d=40 |

- (甲) 增量起点错写成 SP（含）：位点 4 的 `c,+30` 被重复累加，最终 c=60（正确 30）。
- (乙) SP 误记为「下一条要读」：Table 只覆盖 ≤3 缺 c，位点 4 的 `c,+30` 被丢，最终 c=0（正确 30）。
- (丙) 增量起点错写成 SP+2：位点 5 的 `b,+5` 被丢，最终 b=23（正确 28）。

## 二、四条不变量：保证位置与钉住它的测试

1. 与朴素参照一致：`sw.ApplySnapshot` 整体拷贝 Table、`sw.ApplyIncremental` 只动单 key；测试 `api_test.go:TestColdStartMatchesNaive`（多档 SP 循环）。
2. 无缝隙无重叠：快照语义「≤SP 含」+ 增量从 `applied+1` 起，见 `snap.CheckEvent`；测试 `TestNoGapNoOverlap`。
3. 连续单调：`sw.ApplyIncremental` 先校验 `ev.Pos == applied+1` 再推进 `applied=ev.Pos`；测试 `TestRejectsNonContiguous`。
4. 失败不留痕：所有校验（`snap.CheckSnapshot`/`CheckEvent`）先于任何写操作，拒绝即 return；测试 `TestRejectionLeavesNoTrace`。

复杂度：`sw.Switcher.scanned` 非导出计数器由 `sw` 包内测试 `sw_test.go:TestIncrementalIsConstant` 钉住（m=100…10000 档，计数 ≤1）。
并发：`View`/`SelfCheck` 走 RWMutex 读锁或由全新实例构成，测试 `api_test.go:TestConcurrentView`。
