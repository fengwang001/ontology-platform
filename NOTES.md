# Notes: event-bus fan-out, slow-consumer isolation

## 六步推导（N=2, C=2；s0/s1 队列与丢弃计数）
| 步 | 操作 | s0 队列 | s1 队列 | d0 | d1 |
|---|---|---|---|---|---|
| 1 | Publish(1) | [1] | [1] | 0 | 0 |
| 2 | Publish(2) | [1,2] | [1,2] | 0 | 0 |
| 3 | Publish(3) | [1,2] | [1,2] | 1 | 1 |
| 4 | Consume(s0)=1 | [2] | [1,2] | 1 | 1 |
| 5 | Publish(4) | [2,4] | [1,2] | 1 | 2 |
| 6 | Consume(s1)=1 | [2,4] | [2] | 1 | 2 |

(甲) head-drop：第 3 步丢最旧的 1 再入 3，s0=[2,3]（正确 [1,2]）。
(乙) 任一满则全体丢：第 5 步 s0 也不收 4，s0=[2]（正确 [2,4]）。
(丙) 满则阻塞（背压）：3 未被丢弃，s0 DropCount=0（正确应为 1）。

## 不变量保证位置与钉住测试
1. 与朴素参照一致：api.go SelfCheck 内置操作序列逐条朴素模拟对照；api_test.go TestNaiveReference。
2. 慢消费者隔离：bus.go Publish 逐订阅者独立 Enqueue，满仅计该订阅者丢弃；TestSlowConsumerIsolation。
3. FIFO 顺序：fanout.go 环形缓冲 head/n，Dequeue 恒取最旧；fanout_test.go TestFIFOOrder。
4. 失败不留痕：api.go New 先校验后构造、各方法先判 si 越界再触状态；TestRejectedOpsLeaveNoTrace。
满判定 O(1)：fanout.go 长度由 n 字段维护，slotsChecked 不随 m 增长；fanout_test.go TestFullCheckSlotsConstant。
并发安全：fanout.go 每队列独立 mutex；api_test.go TestConcurrentConsume（go test -race）。
