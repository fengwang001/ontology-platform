# NOTES — 物化视图双缓冲重建切换

## 第三节：七步分步表（第 0 行为初始）

| 步 | F | B | pending | rebuilding |
|---|---|---|---|---|
| 0 初始 | {a:3,b:1} | nil | [] | 否 |
| 1 StartRebuild | {a:3,b:1} | {} | [] | 是 |
| 2 RebuildStep(a,+1) | {a:3,b:1} | {a:1} | [] | 是 |
| 3 RebuildStep(a,+2) | {a:3,b:1} | {a:3} | [] | 是 |
| 4 Apply(a,+1) | {a:4,b:1} | {a:3} | [a+1] | 是 |
| 5 RebuildStep(b,+1) | {a:4,b:1} | {a:3,b:1} | [a+1] | 是 |
| 6 Apply(a,-2) | {a:2,b:1} | {a:3,b:1} | [a+1,a-2] | 是 |
| 7 CommitSwitch | {a:2,b:1} | nil | [] | 否 |

- (甲) 忘补 pending：F 错成 **{a:3,b:1}**；正确 **{a:2,b:1}**（a:3+1-2=2）。
- (乙) 第 4 步就切换（B={a:3}，缺 b 历史）：补 pending 后 F 错成 **{a:4}**；正确 **{a:4,b:1}**。
- (丙) 第 6 步后 View 应为 **{a:2,b:1}**；误读 B 会错成 **{a:3,b:1}**。

## 第二节：四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`view.View.Apply` 立即改 F 并追加 L；`dbuf.Buffer.Commit` 先重放全部 pending 再换指针 — `TestNaiveReference`。
2. 切换原子性：写操作全程持 `view.View.mu`；`dbuf.Buffer.Commit` 先校验 `stepped==cursor`，补 pending，单次 map 指针替换 — `TestCommitAtomicity`。
3. 前台不中断：`view.View.View` 持 RLock 拷贝 `Front()`（F），重建路径从不返回 B — `TestViewReadsForegroundDuringRebuild`。
4. 失败不留痕：哨兵错误互不相同；Apply 先全量校验后改状态，dbuf 失败直接 return 不改字段 — `TestRejectedOpsLeaveNoTrace`。

另：哈希查找常数复杂度 `TestDoubleWriteLookupBounded`（非导出 `bLookups`）；并发 `go test -race` 由 `TestConcurrentApplyView` 钉住。
