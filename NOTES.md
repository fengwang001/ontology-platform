# NOTES — 保活探测半开连接判定

## 八步推导（idleTimeout=100，probeInterval=30，maxProbes=3）

| # | 操作 | lastActive | lastProbe | probes | dead | 本步结果 |
|---|---|---|---|---|---|---|
| 1 | Activity(0)  | 0   | -   | 0 | 否 | 锚定活动，计数清零 |
| 2 | Tick(100)    | 0   | 100 | 1 | 否 | 100-0=100 左闭空闲，发第 1 次探测 |
| 3 | Tick(130)    | 0   | 130 | 2 | 否 | 130-100=30 左闭到点，发第 2 次 |
| 4 | Activity(145)| 145 | 130 | 0 | 否 | 响应到达，probes 清零（重置） |
| 5 | Tick(245)    | 145 | 245 | 1 | 否 | 245-145=100，重新发第 1 次 |
| 6 | Tick(275)    | 145 | 275 | 2 | 否 | 275-245=30，发第 2 次 |
| 7 | Tick(305)    | 145 | 305 | 3 | 否 | 发第 3 次（maxProbes），**尚未判死** |
| 8 | Tick(335)    | 145 | 305 | 3 | 是 | 335-305=30，等满最后一个间隔，判死 |

- **(甲) 探测若也算活动**：第 2 步发探测时 lastActive 被刷成 100，第 3 步时 130-100=30 < 100 不再空闲，第 2 次探测永不发出，probes 恒为 1、永远到不了 maxProbes —— 保活被探测自我续期，半开连接永远判不死。
- **(乙) 间隔误写成右开 `>`**：第 3 步 30>30 为假而 no-op，第 2 次探测被推迟到 now=131（晚 1 个时间单位）；第 3 次探测与判死时刻整体顺延 1。
- **(丙) 发满 maxProbes 立即判死**：会在第 7 步 Tick(305) 就 dead，漏掉第 3 次探测后整整一个 probeInterval 的应答窗口；对端只是卡顿、应答尚在途中（将在 305～335 之间回复）也会被误判死亡，制造假阳性。

## 四条不变量的落地位置与钉住测试

1. **与朴素参照一致**：`keep/keep.go` 的 `Activity`/`Tick` 逐条规则转移；测试 `TestNaiveReference`（随机交错比对同规则朴素影子模型，`keep/keep_test.go`）与 `TestSpecScenario`（本表八步，`api/api_test.go`）钉住。
2. **判死正确**：`keep/keep.go` `Tick` 末分支仅在 `probes==maxProbes && now-lastProbe>=probeInterval` 置 dead，`Activity` 无条件清零；`TestDeathAndReset` 钉住。
3. **探测时序（左闭）**：`keep/keep.go` 空闲门 `now-lastActive < idleTimeout`、间隔门 `now-lastProbe < probeInterval` 均为严格小于，等号即触发；`TestProbeTiming` 钉住。
4. **失败不留痕**：`keep/keep.go` 的 `clockOK` 与 `New` 参数校验先于任何状态写入，拒绝直接返回哨兵 `ErrDead`/`ErrClockBack`/`ErrInvalidParam`；`TestRejectedOpsNoTrace` 钉住。
