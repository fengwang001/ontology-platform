# NOTES — 拥塞控制窗口推导与不变量

## 八步推导（ssthresh=4，maxCwnd 足够大）

| 步 | 操作 | cwnd | ssthresh | state | partial |
|---|---|---|---|---|---|
| 0 | 初始 | 1 | 4 | SlowStart | 0 |
| 1 | OnAck | 2 | 4 | SlowStart | 0 |
| 2 | OnAck | 3 | 4 | SlowStart | 0 |
| 3 | OnAck | 4 | 4 | SlowStart | 0 |
| 4 | OnAck | 4 | 4 | CongAvoid | 1 |
| 5 | OnAck | 4 | 4 | CongAvoid | 2 |
| 6 | OnAck | 4 | 4 | CongAvoid | 3 |
| 7 | OnAck | 5 | 4 | CongAvoid | 0 |
| 8 | OnLoss | 1 | 2 | SlowStart | 0 |

- (甲) 若误写成 `cwnd > ssthresh`（右开）：第 4 步 cwnd=4 不 >4，不切换，仍 SlowStart 且 cwnd+1 → 错成 cwnd=5 / SlowStart（正确：cwnd=4 / CongAvoid）。
- (乙) 若 CongAvoid 误成每 ACK +1：第 4~7 步各 +1，第 7 步后 cwnd 错成 8（正确：5）。
- (丙) `5/2` 向上取整得 3（正确：2，向下取整）。若无下限 `max(·,2)`：cwnd=3 丢包 ssthresh=1，cwnd=2 丢包也得 ssthresh=1；ssthresh=1 时 cwnd=1 即满足 `cwnd>=ssthresh`，慢启动第一步就切 CongAvoid，慢启动名存实亡，窗口每 RTT 只 +1，恢复极慢且阈值失去意义。

## 四条不变量落点

1. 与朴素参照一致：`cwnd/cwnd.go` 的 `AckPreview`/`ApplyAck`/`ApplyLoss` 逐条实现题目规则；测试 `TestNaiveReference`（api_test.go，多种 ssthresh × 随机交错序列逐步对拍）。
2. AIMD 正确：慢启动 +1 与加性增在 `cwnd.go ApplyAck` 两分支，减半在 `ApplyLoss`；测试 `TestEightStep`、`TestNaiveReference`。
3. 切换正确：`cwnd>=ssthresh` 左闭在 `AckPreview` 的 SlowStart 分支，切换置 partial=1 且只发生一次；测试 `TestEightStep`（第 4 步）。
4. 失败不留痕：`cc/cc.go OnAck/OnLoss` 先 Preview/AtFloor 判定，拒绝时不调用任何 Apply；测试 `TestErrors`（拒绝前后四字段不变且可继续用）。
