# NOTES — ontology-348（B=3 H=4 L=2；@后为 Arrival）

十二步表（触发/flush、拒收）：
1  Write(a,1): now0 缓冲[a@0] 无触发 flush无 未拒
2  Tick: now1 缓冲[a@0](age1<2) 无flush 未拒
3  Tick: now2 缓冲[] 延迟触发(age=2) flush[a] 未拒
4  Write(b,2): now2 缓冲[b@2] 无 未拒
5  Write(c,3): now2 缓冲[b@2,c@2] 无 未拒
6  Write(d,4): now2 缓冲[b@2,c@2,d@2] 无 未拒
7  Tick: now3 缓冲[] 批量触发(len=3) flush[b,c,d] 未拒
8  Write(e,5): now3 缓冲[e@3] 无 未拒
9  Write(f,6): now3 缓冲[e@3,f@3] 无 未拒
10 Write(g,7): now3 缓冲[e@3,f@3,g@3] 无 未拒
11 Write(h,8): now3 缓冲[e@3,f@3,g@3,h@3] 无 未拒
12 Write(i,9): now3 缓冲不变[e@3,f@3,g@3,h@3] 无flush **拒收**(len=H=4)

(甲) 误写 age>L：第3步 age=2 不触发，正确应 flush [a]，错误实现 flush 无；a 拖到第3次 Tick(now3,age3) 才被冲走。第3步结束 age=2 已违反不变量3（Tick 后须 age<L=2），滞留期间 age 最大到 3。
(乙) 误写 len>H：第12步 len=4 时 i 被误收，缓冲变 [e,f,g,h,i] 共 5 条，高水位被突破 1 条（H=4 却装到 5）。
(丙) 第2批[d,e,f]失败若只留 f 回缓冲、d,e 留在 Sink：Sink=a,b,c,d,e，重试整批后 d、e 两个 key 被收到两次（a,b,c,d,e,d,e,f）。正确实现：缓冲恢复[d@1,e@1,f@1]、Sink 只收[a,b,c]、delivered=3、now 回到 1；重试恰收 d,e,f 各一次，最终 a,b,c,d,e,f 不丢不重。

不变量 → 代码保证位置 / 钉住的测试函数：
I1 朴素重放一致：bflush.flushHead 与 Tick 排水只经 buf.Buffer.Take 取头部、Apply 成功才计 delivered — TestNaiveReplay、TestConcurrentWrites
I2 每批≤B：flushHead 唯一出口，n 只取 B(批量)/len(延迟)/min(B,len)(Flush) — TestBatchBound
I3 Tick 后 age<L：bflush.Tick 先 now++ 再按「批量优先、否则 age>=L」排水到两条件皆不成立 — TestLatencyBound
I4 失败不留痕：Write 先判空 key/高水位再 Push；flushHead 失败 Prepend 整批按序还头、Tick 同时把 now 恢复到调用前 — TestRejectNoTrace、TestSinkFailureRollback
