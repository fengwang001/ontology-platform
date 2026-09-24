# 限速回放器 NOTES

rate=2 cap=5 burst=10 maxQ=100；初态 now=0 tokens=10 队列空。注意 Advance 参数是绝对时刻。

| # | 操作 | now | tokens | 队列 | 吐出 |
|---|---|---|---|---|---|
| 1 | Enqueue 1..15 | 0 | 10 | [1..15] | — |
| 2 | Emit | 0 | 0 | [11..15] | [1..10] |
| 3 | Advance(1) | 1 | 5 | [11..15] | —（dt=1，开始时非空→cap，补5）|
| 4 | Emit | 1 | 0 | [] | [11..15] |
| 5 | Advance(2) | 2 | 2 | [] | —（dt=1，空→rate，补2）|
| 6 | Emit | 2 | 2 | [] | [] |
| 7 | Enqueue 16,17,18 | 2 | 2 | [16,17,18] | — |
| 8 | Emit | 2 | 0 | [18] | [16,17] |

(甲) 第2步吐10个；第3步开始时队列非空，按 cap 补5。若忽略追赶恒按 rate：只补2，第4步仅吐[11,12]两个，队剩[13,14,15]。
(乙) 初始 tokens=burst=10，每事件扣1，无时间补充，故最多即时吐10而非15。burst 当无限则第2步吐15；突发拆平由 burst 保证。
(丙) 再 Advance(3)：dt=1、[18]非空→按cap补5，Emit 吐出18（会）。把 cap 错当每次 Emit 的次数上限→第2步错吐5个[1..5]。
守恒视角：速率上限只决定积压"多快"清空（时间足够必全清，不丢不重）；次数上限随 Emit 调用切分而变，与历史三元组不符，破坏不变量1。

不变量1（朴素一致）：tok/tok.go 的 Advance/Take 与 pace/pace.go Emit（min(len)+整段切片）；TestEightStepGolden、TestNaiveEquivalenceRandom
不变量2（追赶封顶）：pace/pace.go Advance 在补充前以 len(q)>0 选 rate/cap，tok/tok.go 封顶 burst；TestCatchUpRate
不变量3（守恒）：api/api.go SelfCheck→replay 逐步核对 入==出+len(队列)，pace/pace.go Enqueue 只在整体成功时 append、Emit 切片数==扣令牌数；TestConservation
不变量4（失败不留痕）：pace/pace.go New 与 tok/tok.go New 先校验后构造，Enqueue/Advance 命中哨兵即返回不改状态；TestRejectedOpsLeaveNoTrace
复杂度：pace.scanned 恒为0（吐出个数只由 len 决定，逐条拷贝不算判定遍历）；TestEmitDecisionIsOOne。并发只读一致：TestConcurrentReaders。
