# 读时一致性快照 — 推导与不变量落点

## 第三节：八步分步表（初值全 0）

| 步 | 操作 | 操作后 ver | hist[a] | hist[b] | 读/快照结果 |
|---|---|---|---|---|---|
| 1 | Write(a,10) | 1 | (1,10) | — | |
| 2 | Write(b,20) | 2 | (1,10) | (2,20) | |
| 3 | s1=Snapshot() | 2 | (1,10) | (2,20) | s1=2 |
| 4 | Write(a,15) | 3 | (1,10),(3,15) | (2,20) | |
| 5 | Read(s1,a) | 3 | (1,10),(3,15) | (2,20) | 10 |
| 6 | Write(b,25) | 4 | (1,10),(3,15) | (2,20),(4,25) | |
| 7 | Read(s1,b) | 4 | (1,10),(3,15) | (2,20),(4,25) | 20 |
| 8 | s2=Snapshot() | 4 | (1,10),(3,15) | (2,20),(4,25) | s2=4 |

- (甲) 正确为 **10**（a 的版本 1）。若不保留版本历史、只存最新值，会错成 **15**（读到版本 3 的值）。
- (乙) 正确为 **20**（b 的版本 2，恰等于 s1=2，`<=` 取等）。若误写成 `ver < snap`，b 无 ver<2 的条目，会错成 **0**。
- (丙) 若误用读取时刻最新版本（ver=4），会错成 **25**（b 的版本 4）。此时第 5 步 a 按 ver=2 读、第 7 步 b 按 ver=4 读，同一快照的视图混杂两个时刻，故不变量 2 要求同一快照内所有 Key 共用同一固定版本。

## 第二节：四条不变量落点

1. 与朴素参照一致：`snap/snap.go` 的 `Store.Read` 二分取 `ver<=snap` 的最大版本，等价于重放后写覆盖先写；测试 `TestReadMatchesNaiveReplay`。
2. 快照隔离（跨键一致）：快照 id 在 `api.Snapshot`→`snap.Store.Snapshot` 时固定，`Store.Read` 只按入参 snap 比较、与当前 ver 无关；测试 `TestSnapshotIsolationCrossKey`。
3. 读不阻塞写：`Store.Read`/`Snapshot`/`ReadCurrent` 不改 ver 与任何历史，`Store.Write` 单调 ver++；测试 `TestReadSnapshotNoSideEffect`。
4. 失败不留痕：`api/api.go` 各方法先校验（`ver.Valid`、空 Key、上限、存活登记）再动状态，拒绝路径零写入；测试 `TestRejectedOpsLeaveNoTrace`。
