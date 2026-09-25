# NOTES — 读己之写会话一致性

初始：R0/R1/R2 对 k 均不存在（记 `-`，即 ("",0)），writeVer[k]=0。

| # | 操作 | R0 | R1 | R2 | wv | Read 返回（来源/理由） |
|---|---|---|---|---|---|---|
| 1 | Write(S,k,A) | (A,1) | - | - | 1 | - |
| 2 | Read(S,k) | (A,1) | - | - | 1 | (A,1) @R0（R1 ver0 < 1） |
| 3 | Sync(1) | (A,1) | (A,1) | - | 1 | - |
| 4 | Write(S,k,B) | (B,2) | (A,1) | - | 2 | - |
| 5 | Sync(2) | (B,2) | (A,1) | (B,2) | 2 | - |
| 6 | Write(S,k,C) | (C,3) | (A,1) | (B,2) | 3 | - |
| 7 | Read(S,k) | (C,3) | (A,1) | (B,2) | 3 | (C,3) @R0（R1 ver1 < 3；只查粘滞 R1） |

- (甲) 不检查 wv、固定读 R1：第 2 步 R1 无 k → 得到 ("",0)/不存在；正确 (A,1) @R0。
- (乙) wv 记成 v-1（第 7 步 wv=2）：R2=(B,2) 满足 2>=2，被误判已追平 → 读到 (B,2)；正确 (C,3) @R0。R1 ver1 仍不足，陷阱专由第 5 步 Sync(2) 制造。
- (丙) 缓存第 3 步“R1 已追平”结论：第 7 步仍读 R1 → (A,1)；正确 (C,3) @R0。故每次写更新 wv、每次读现算，禁止缓存。

## 四条不变量：保证位置 / 钉住测试

1. 读己之写：`ses.Read` 用粘滞副本 `Entry.AtLeast(wv)` 判定，不足即回 R0 —— `TestReadYourWrites`。
2. 朴素参照一致：回 R0 取权威值；副本仅在 ver>=wv 时服务且内容是 R0 的整表快照，`View`=R0 Snapshot —— `TestReferenceConsistency`。
3. 版本单调：`kv.Master.Advance` 只 `ver[key]++`；`Sync` 用 `Load(Snapshot())` 整表替换，版本只随主副本前进不回退 —— `TestMonotonicAndSync`。
4. 失败不留痕：空 key / idx 越界 / 已关闭均在任何状态变更前返回哨兵错误（`ses.Write`/`Read`/`Sync`）—— `TestRejectedOpsLeaveNoTrace`。

复杂度：非导出 `ses.Cluster.checks` 记录最近一次路由检查的 (副本,key) 条目数；路由只定位 1 条，白盒 `ses` 包测试 `TestRouteChecksConstant`（m=100..10000 恒为 1）。
并发：`sync.RWMutex`（读 RLock），`TestConcurrentReadersIdenticalView`；自检 `api.Store.SelfCheck`，`TestSelfCheck`。
