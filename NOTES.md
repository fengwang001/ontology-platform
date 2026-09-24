# NOTES — 事件总线广播（N=2, C=2 六步推导）

| 步 | 操作 | s0 队列 | s1 队列 | d0 | d1 |
|---|---|---|---|---|---|
| 1 | Publish(1) | [1] | [1] | 0 | 0 |
| 2 | Publish(2) | [1,2] | [1,2] | 0 | 0 |
| 3 | Publish(3) | [1,2] | [1,2] | 1 | 1 |
| 4 | Consume(s0)=1 | [2] | [1,2] | 1 | 1 |
| 5 | Publish(4) | [2,4] | [1,2] | 1 | 2 |
| 6 | Consume(s1)=1 | [2,4] | [2] | 1 | 2 |

- (甲) head-drop：第 3 步丢最旧的 1 再入 3，s0 错成 `[2,3]`（正确 `[1,2]`）。
- (乙) 任一满则全体丢：第 5 步 s0 也收不到 4，错成 `[2]`（正确 `[2,4]`）。
- (丙) 满则阻塞（背压）：3 未被丢弃，s0 DropCount 错成 `0`（正确 `1`），且 Publish 挂死。

## 四条不变量：保证位置 / 钉住的测试

1. 与朴素参照一致：`bus.Publish` 逐订阅者调 `fanout.Queue.Offer`（未满入尾、满则丢新并累加）；测试 `TestNaiveReference`、`TestSixStepScenario`。
2. 慢消费者隔离：`bus.Publish` 对每个订阅者独立 Offer，`Offer` 只做 O(1) 字段比较、绝不阻塞；测试 `TestSlowConsumerIsolation`。
3. FIFO 顺序：`fanout.Queue` 用 ring(head,count)，Offer 写尾、Poll 取头；测试 `TestFIFOOrder`。
4. 失败不留痕：`api.New` 先校验 N/C 再分配任何状态；`Consume/DropCount/QueueLen` 先越界判定（哨兵 error panic）后触状态；测试 `TestSentinelErrors`、`TestRejectedOpsLeaveNoTrace`。

槽位计数 `slotChecks` 是 fanout 非导出字段，`TestSlotCheckBoundAcrossSizes` 钉住其不随 m 增长；并发由 `TestConcurrentConsume`（`go test -race`）钉住。越界错误以哨兵 error 作 panic 值（签名无 error 返回位），recover 后 `errors.Is` 可判定。
