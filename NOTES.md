# 带检查点的物化视图幂等重建 — 推导与不变量

## 第三节推导：src=["a","b","a","c","b","a"]，chunk=2（cp=检查点）

| # | 操作 | processed | shadow | cp | committed(视图) | gen |
|---|-------|-----------|--------|----|----------------|-----|
| 1 | New    | 0 | {} | {0,{}} | {} | 0 |
| 2 | Start  | 0 | {} | {0,{}} | {} | 0 |
| 3 | Step   | 2 | {a:1,b:1} | {2,{a:1,b:1}} | {} | 0 |
| 4 | Step   | 4 | {a:2,b:1,c:1} | {4,{a:2,b:1,c:1}} | {} | 0 |
| 5 | Crash  | 4 | （丢弃） | {4,{a:2,b:1,c:1}} | {} | 0 |
| 6 | View   | 4 | — | {4,{a:2,b:1,c:1}} | {} | 0 |
| 7 | Start  | 4 | {a:2,b:1,c:1}（从 cp 恢复） | {4,{a:2,b:1,c:1}} | {} | 0 |
| 8 | Step   | 6 | {a:3,b:2,c:1} | {6,{a:3,b:2,c:1}} | {} | 0 |
| 9 | Commit | 6 | {a:3,b:2,c:1} | {6,{a:3,b:2,c:1}} | {a:3,b:2,c:1} | 1 |

- (甲) 第 6 步 View() 必须返回空映射 {}（gen=0）。「在 committed 上原地重建」的实现第 4 步后会让 View() 返回半成品 {a:2,b:1,c:1}——把未提交的 shadow 泄给了读者，错。
- (乙) 若 Start 把 processed 归零却保留 shadow={a:2,b:1,c:1} 再从头重放整个 src（再加 {a:3,b:2,c:1}），最终 Commit 的视图错成 {a:5,b:3,c:2}；正确视图是 {a:3,b:2,c:1}。
- (丙) 第 4 步后（processed=4<6）调 Commit：正确实现返回 ErrIncomplete，视图仍 {}、gen 仍 0，状态不变。「不检查完成度就切换」的实现会把视图错切成 {a:2,b:1,c:1}、gen 错成 1。

## 四条不变量：保证位置 + 钉住它的测试

1. 与朴素重放一致：Step 只经 agg.Apply 把 src 片段计入 shadow，Commit 整体拷贝 shadow→committed（reb/reb.go 的 Step/Commit）；测试 TestMatchesNaiveReplay。
2. 重建期间旧视图可读：View/Gen 只读 committed，committed 仅在 Commit 内被替换（reb/reb.go 的 View/Gen/Commit）；测试 TestOldViewReadableDuringRebuild。
3. 崩溃可续且不重复不丢失：Crash 不动检查点，Start 从 cp 恢复 shadow 与 processed，非导出计数器 applied 只增不重放已检查点项，≤ n+c·chunk（reb/reb.go 的 Crash/Start/Step）；测试 TestCrashResumeNoDupNoLoss（同函数内断言计数器上界）。并发完整性由 TestConcurrentViewConsistency 钉住。
4. 失败不留痕：Start/Step/Commit/Crash/New 全部先校验、拒绝时直接返回哨兵错误，不触碰任何字段（reb/reb.go 各前置检查）；测试 TestRejectedOpsLeaveStateUnchanged。
