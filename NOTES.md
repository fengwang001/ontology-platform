# 双缓冲重建切换 NOTES

## 第三节：七步推导

初始 `L = [(a,+1),(a,+2),(b,+1)]`，`F = {a:3,b:1}`。

| 步 | 操作 | F | B | pending | rebuilding |
|---|---|---|---|---|---|
| 0 | 初始 | {a:3,b:1} | nil | [] | false |
| 1 | StartRebuild() | {a:3,b:1} | {} | [] | true |
| 2 | RebuildStep(a,+1) | {a:3,b:1} | {a:1} | [] | true |
| 3 | RebuildStep(a,+2) | {a:3,b:1} | {a:3} | [] | true |
| 4 | Apply(a,+1) | {a:4,b:1} | {a:3} | [(a,+1)] | true |
| 5 | RebuildStep(b,+1) | {a:4,b:1} | {a:3,b:1} | [(a,+1)] | true |
| 6 | Apply(a,-2) | {a:2,b:1} | {a:3,b:1} | [(a,+1),(a,-2)] | true |
| 7 | CommitSwitch() | {a:2,b:1} | nil | [] | false |

- (甲) 忘把 pending 应用到 B：切换后 `F` 错成 `{a:3,b:1}`；正确应为 `{a:2,b:1}`。
- (乙) 第 4 步即 CommitSwitch：`B={a:3}` 补 pending `(a,+1)` 后 `F` 错成 `{a:4}`（丢 `b:1`）；正确应为 `{a:4,b:1}`。
- (丙) 第 6 步后 View 应返回 `{a:2,b:1}`；若误读 B 则错成 `{a:3,b:1}`。

## 四条不变量落实位置

1. 与朴素参照一致：`dbuf.Commit` 先把 pending 全部回放进 B 再换前台指针；测试 `TestAgainstNaive`。
2. 切换原子性：`view.CommitSwitch` 持写锁一次完成「补齐+换针+清场」，读者无中间态；测试 `TestSwitchAtomicity`。
3. 前台不中断：`view.View` 只读 F，重建期间 `Apply` 照旧增量更新 F；测试 `TestViewDuringRebuild`。
4. 失败不留痕：`view` 各入口先整体校验再改动（Apply 批量先验后写）；测试 `TestRejectedNoOp`。
