# NOTES（限速回放器）

参数 rate=2 cap=5 burst=10 maxQueue=100；初态 now=0 tokens=10 Q=[]。Advance(t) 的 t 为绝对逻辑时间，dt=t-now。

| 步 | 操作 | now | tokens | 队列 | 吐出 |
|---|---|---|---|---|---|
| 1 | Enqueue(1..15) | 0 | 10 | [1..15] | — |
| 2 | Emit() | 0 | 0 | [11..15] | [1..10] |
| 3 | Advance(1) | 1 | 5 | [11..15] | dt=1，开始时队非空→cap，补 5 |
| 4 | Emit() | 1 | 0 | [] | [11..15] |
| 5 | Advance(2) | 2 | 2 | [] | dt=1，开始时队空→rate，补 2 |
| 6 | Emit() | 2 | 2 | [] | []（无令牌也无积压） |
| 7 | Enqueue(16,17,18) | 2 | 2 | [16,17,18] | — |
| 8 | Emit() | 2 | 0 | [18] | [16,17] |

甲：步2吐 10 个；步3补 5 令牌，按 cap。若忽略追赶总按 rate：步3只补 2，步4只吐 [11,12] 共 2 个，队列剩 [13,14,15]。
乙：初始 tokens=burst=10（满桶），每事件扣 1，tokens=0 即停，故只吐 10；burst 当无限则步2吐 15。拆平突发由 burst（瞬时上限）配合 rate/cap（时间维补充）共同保证。
丙：再 Advance(3)：dt=1、队非空按 cap 补 5，Emit 吐出 [18]，会。若把 cap 误当每次 Emit 次数上限 5，步2会错吐 5 个 [1..5]。cap 封顶的是单位时间补充速率（与调用频率无关），Emit 唯一上界是 tokens；守恒只认「令牌扣减」，未付费事件原样留队，故封顶必须是速率而非次数。

不变量保证位置与钉住测试：
1. 朴素一致：api/api.go 的 naiveModel 与 (*Pacer).SelfCheck 逐操作比对（now/tokens/Pending/Emit 序列）——TestNaiveReplay。
2. 追赶封顶：tok/tok.go 的 (*Bucket).Advance 按开始时刻 backlog 选 cap/rate，补后 min(burst) 封顶——TestCatchUpCap。
3. 守恒：pace/pace.go 的 Queue.in/out，Enqueue 整批验收后加 in、Emit 切片后加 out——TestConservation。
4. 失败不留痕：api/api.go 的 New 先校验后构造；pace/pace.go Enqueue 先判超限后追加；tok/tok.go Advance 先判 t<now 直接返回——TestRejectedOpsLeaveNoTrace。
