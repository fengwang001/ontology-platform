# 连接拆除状态机 NOTES

## 七步推导（2MSL=100，初始 ESTABLISHED）

| # | 事件(now) | 之后状态 | 动作 | enterTime |
|---|---|---|---|---|
| 1 | Close() | FIN_WAIT_1 | 发 FIN | — |
| 2 | RecvACK() | FIN_WAIT_2 | 无 | — |
| 3 | RecvData() | FIN_WAIT_2（不变） | 无 | — |
| 4 | RecvFIN(100) | TIME_WAIT | 回 ACK | 100 |
| 5 | RecvFIN(150) | TIME_WAIT（不变） | 回 ACK | 150（重启） |
| 6 | Tick(249) | TIME_WAIT（249-150=99<100） | 无 | 150 |
| 7 | Tick(250) | CLOSED（250-150=100>=100，左闭） | 无 | — |

**(甲)** 合法，状态不变（FIN_WAIT_2/CLOSE_WAIT 收数据均合法）。若误拒，违反半关闭语义：本端已发 FIN 后对端仍可能留有未发完的数据，误拒会丢失这批数据并把对端误判为违规，拆除流程无法按序走完。

**(乙)** 不重启则 enterTime 保持 100，第 6 步 Tick(249)：249-100=149>=100，本端提前进入 CLOSED。对端随后重传的 FIN 撞上 CLOSED 被判非法、收不到 ACK，被误判为无效报文，对端将永远等不到 LAST_ACK 的确认而卡在 LAST_ACK。

**(丙)** 同时关闭：A: Close→FIN_WAIT_1；收 B 的 FIN→CLOSING（回 ACK）；收 B 的 ACK→TIME_WAIT（起 2MSL）；Tick 超时→CLOSED。B 完全对称。若「FIN_WAIT_1 收 FIN」被误当 ACK 处理：A→FIN_WAIT_2 且未回 ACK——A 把对端唯一的 FIN 误吞，自己仍在等对端 FIN（本题无重传语义）永远等不到，永久卡在 FIN_WAIT_2；B 也因收不到 ACK 卡在 FIN_WAIT_1，连接永久泄漏。

## 四条不变量：保证位置 / 钉住测试

1. 与转移表一致：`conn.table` 是唯一事实源，`Conn.Apply` 每事件查表 1 次并执行 — `TestTransitionTable`。
2. 半关闭可收数据、CLOSED 拒绝报文：table 中 FIN_WAIT_2/CLOSE_WAIT 的 EvData 自环、CLOSED 无任何表项 — `TestHalfCloseAndClosed`。
3. TIME_WAIT 计时正确：`Apply` 的 expire 分支（now-enter>=2MSL 左闭才离开）与 EvFIN 命中时 enter=now 重启 — `TestTimeWaitRules`。
4. 失败不留痕：`Apply` 先校验（时钟、查表）后写入，错误路径零副作用 — `TestIllegalNoSideEffect`。
