# NOTES: heartbeat liveness（timeout=10, idleWindow=30，边界左闭右开）

## 第三节：八步分步表（同一流 s）

| # | 操作 | 后 lastHb | age | 返回 |
|---|---|---|---|---|
| 1 | Heartbeat(s,0) | 0 | - | 接受 |
| 2 | Status(s,10) | 0 | 10 | active |
| 3 | Status(s,11) | 0 | 11 | idle |
| 4 | Status(s,30) | 0 | 30 | idle |
| 5 | Status(s,31) | 0 | 31 | dead |
| 6 | Heartbeat(s,40) | 40 | - | 接受（dead 后续接恢复） |
| 7 | Heartbeat(s,20) | 40 | - | 忽略（20<40 过期） |
| 8 | Status(s,55) | 40 | 15 | idle |

- (甲) 若 dead 写成 `age >= 30`：第 4 步 age=30 会错判 **dead**；正确是 `age > 30`，age=30 属 **idle**。
- (乙) 若过期心跳无条件覆盖：lastHb 变 20，第 8 步 age=55-20=35 错判 **dead**；正确 lastHb=40、age=15、**idle**。
- (丙) 若 dead 即永久标记移除：第 6 步被拒，Status(s,40) 错为 **dead/absent**；正确是接受心跳，age=0，**active**。

## 第二节：四条不变量 → 代码保证位置 / 钉住的测试

1. 心跳单调：`hb.Stream.Observe` 仅 `ts >= last` 才写 lastHb；测试 TestHeartbeatMonotonic。
2. 朴素参照一致：`hb.Stream.State` 的 `age<=10 / <=30 / >30` 三分支；测试 TestNaiveReference、TestHBBoundaryTable。
3. 状态转移正确：接受心跳即 age 归零，dead 条目在 Manager 中重入到期堆；测试 TestTransitions、TestRecovery。
4. 失败不留痕：`Manager.Heartbeat/State/Sweep` 先校验（空 id/负 ts/负 now/回拨）后改表，新流负 ts 不入 map；测试 TestRejectedOpsLeaveNoTrace、TestAPISentinelErrors。
