# ontology-348 NOTES：B=3 H=4 L=2，Sink 恒成功；`a0` 表示 a 的 Arrival=0，每步前先执行本步操作
| 步 | 操作 | now | 缓冲(Arrival) | 触发/flush 出 | 被拒 |
|---|---|---|---|---|---|
| 1 | Write(a,1) | 0 | a0 | 无 / 无 | 否 |
| 2 | Tick | 1 | a0 | 无（age=1<2） | 否 |
| 3 | Tick | 2 | 空 | 延迟 / [a]（age=2>=2） | 否 |
| 4 | Write(b,2) | 2 | b2 | 无 / 无 | 否 |
| 5 | Write(c,3) | 2 | b2 c2 | 无 / 无 | 否 |
| 6 | Write(d,4) | 2 | b2 c2 d2 | 无 / 无 | 否 |
| 7 | Tick | 3 | 空 | 批量 / [b,c,d]（len=3>=B，批量优先） | 否 |
| 8 | Write(e,5) | 3 | e3 | 无 / 无 | 否 |
| 9 | Write(f,6) | 3 | e3 f3 | 无 / 无 | 否 |
| 10 | Write(g,7) | 3 | e3 f3 g3 | 无 / 无 | 否 |
| 11 | Write(h,8) | 3 | e3 f3 g3 h3 | 无 / 无 | 否 |
| 12 | Write(i,9) | 3 | e3 f3 g3 h3 | 无 / 无（len=4>=H） | 是 |
甲：若延迟触发写成 age>L，第 3 步 flush 无（正确应 flush [a]）；a 要到第 3 次 Tick（第 7 步）才走，且因批量优先被并入批量 [a,b,c] 冲走，冲走前 age 最大到 3；第 3 步 Tick 结束时 a 的 age=2 不满足 <L，直接违反不变量 3（延迟上界），FIFO 拼接顺序也被扭曲。
乙：若拒收写成 len>H，第 12 步 i 会被收下，缓冲装到 5 条（e f g h i），高水位被突破 1 条（拒收滞后一拍，上界变 H+1），违反高水位背压与并发读到的 Buffered∈[0,H] 界。
丙：部分成功时 d、e 已留在 Sink，重试整批 [d,e,f] 后 d、e 会被收到两次（f 仅一次）；正确实现失败批按原顺序回滚，缓冲恢复 [d,e,f]、Sink 只收到 [a,b,c]、now 回滚到 1，重试后 a..f 各恰好一次。
不变量保证位置 / 钉住的测试：
1 朴素重放：所有 flush 只经 buf.(*Buffer).take 取 FIFO 头部、bflush.Engine.FlushAll 排空 —— TestNaiveReplay（随机 Write/Tick/Flush 序列比对）。
2 批量上界：buf.take 的 n 恒 ≤B（TakeTick 批量取 B；TakeFlush 取 min(B,len)）—— TestBatchBound。
3 延迟上界：buf.TakeTick 以 age>=L 判定且 bflush.Tick 循环到两触发皆不成立才返回 —— TestLatencyBound（模型镜像逐 Tick 核验）。
4 失败不留痕：bflush.Engine.Write 先判空 key/高水位再改状态；Tick/Flush 失败时 buf.(*Buffer).Return 整批放回头部且 Tick 的 now 还原 old —— TestFailureNoTrace；访问计数 buf.scan 为 O(B) 由 TestTakeComplexity 钉住；并发由 TestConcurrentWrites（-race）钉住。
