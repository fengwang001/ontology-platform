# NOTES — 带检查点的变更流恢复点

## 第三节：八步推导（初始全零、ckpt 无）

| 步 | 操作 | applied | flushed | ckpt | total |
| 1 | Apply(1,+5) | 1 | 0 | 无 | 5 |
| 2 | Apply(2,+3) | 2 | 0 | 无 | 8 |
| 3 | Flush() | 2 | 2 | 无 | 8 |
| 4 | Apply(3,-1) | 3 | 2 | 无 | 7 |
| 5 | Checkpoint() | 3 | 2 | (2,8) | 7 |
| 6 | Apply(4,+6) | 4 | 2 | (2,8) | 13 |
| 7 | Apply(5,+2) | 5 | 2 | (2,8) | 15 |
| 8 | Restart() | 2 | 2 | (2,8) | 8 |

恢复后从 ckpt.pos+1=3 重放位点 3,4,5（-1+6+2=+7）→ total=15，与无崩溃一路 Apply 到底一致。

- (甲) 错把 Checkpoint 写成 `(flushed, 当前 total)`：第 5 步写出 ckpt=(2,7)（7 已含位点3的 -1）。Restart 后 total=7、applied=2，重放 3,4,5 又加一遍 -1 → total=14（应 15）。这是「重」：位点 3 的 Delta -1 被计了两次。
- (乙) 错把 ckpt 的 pos 写成 applied：第 5 步写出 (3,8)，pos 声称 3 已持久而 total 不含它。Restart 后 applied=3，只重放 4,5 → total=8+6+2=16（应 15）。这是「丢」：位点 3 的 Delta -1 丢失。
- (丙) 重放起点错写成 ckpt.pos（含）：重放 2,3,4,5 → total=8+3-1+6+2=18（应 15）。这是「重」：位点 2 的 Delta +3 被计了两次。

## 第二节：四条不变量的保障位置与钉住测试

1. 与朴素参照一致：`ckp.State.Restart` 精确重置到 ckpt，调用方自 ckpt.pos+1 重放（ckp/ckp.go、rec/rec.go）；api.SelfCheck 不变量1 — 测试 `TestRestartEquivalence`、`TestSelfCheck`。
2. 快照一致切面：`ckp.State.Checkpoint` 只写 `(flushed, flushTotal)`，flushTotal 是 Flush 时刻 total 的快照（ckp/ckp.go）— 测试 `TestEightStepSequence`、`TestRestartEquivalence`。
3. 边界与单调：`ckp.State.Checkpoint` 仅在 `flushed > ckpt.pos` 时写入，写的就是 flushed — 测试 `TestRestartEquivalence`（逐步断言）。
4. 失败不留痕：`ckp.State.Apply`/`Restart` 先校验、出错直接返回哨兵错误，不触碰任何字段 — 测试 `TestSentinelErrors`。
