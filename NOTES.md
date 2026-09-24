# NOTES

约定：`e`=Applied，`x`=DeadLettered；阻塞记 `键:队首/已尝试`；缓冲只列非空。fails={A2:1,B1:99,B3:1}，maxAttempts=3，maxBuffered=8。

| 步 | 本步定案 | 阻塞(队首/次数) | 缓冲队列 | 死信 |
|---|---|---|---|---|
|1 Submit A1|A1e|无|空|空|
|2 Submit A2|无|A:A2/1|空|空|
|3 Submit B1|无|A:A2/1,B:B1/1|空|空|
|4 Submit A3|无|A:A2/1,B:B1/1|A:[A3]|空|
|5 Submit C1|C1e|A:A2/1,B:B1/1|A:[A3]|空|
|6 Submit B2|无|A:A2/1,B:B1/1|A:[A3],B:[B2]|空|
|7 Tick|A2e,A3e|B:B1/2|B:[B2]|空|
|8 Submit B3|无|B:B1/2|B:[B2,B3]|空|
|9 Submit A4|A4e|B:B1/2|B:[B2,B3]|空|
|10 Tick|B1x,B2e|B:B3/1|B:空|[B1]|
|11 Tick|B3e|无|空|[B1]|

(甲) 全局阻塞：第5步 C1 只能进全局缓冲（未尝试、未定案）；第5步末已应用仅 1 条（A1），正确实现 2 条（A1,C1）；违反不变量2（最终排干后次序未必错，错的是 Submit 内必须立即应用）。
(乙) 失败不阻塞本键：A4 之前 A3 在第4步即被立即应用，A2 在第7步 Tick 才成功，故 A 次序 A1,A3,A2,A4；正确为 A1,A2,A3,A4。
(丙) LIFO 排空：第10步仅 B1x（B3 首试失败成新队首/1，B2 仍留缓冲）；队首 B3/1、缓冲 [B2]；B 最终次序 B1x,B3e,B2e；正确为 B1x,B2e,B3e。

不变量保证位置 / 钉住测试：
1 同键保序：Submit 序号校验 + kq 单队首 FIFO 排空（applier.go Tick/drainLocked）→ TestInvariantPrefixOrder、TestSerialReference
2 故障隔离：阻塞集合按 Key 划分，Submit 只查本键状态（applier.go Submit）→ TestFaultIsolation、TestElevenSteps
3 串行参照：Tick 开始快照阻塞键并按字典序、每键一次、FIFO 排空（applier.go Tick）→ TestSerialReference、TestElevenSteps
4 失败不留痕：New/Submit 全部校验先于任何状态变更（applier.go New、Submit）→ TestRejectionNoTrace、TestSentinelErrors
