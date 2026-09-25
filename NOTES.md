# NOTES — ontology-469（limit=3 八步推导；R 常驻 / S 溢写，尾列 spills/loads）

1 插入 (a,5)     R{a:5}             S{}      溢写- (0/0)
2 插入 (b,3)     R{a:5,b:3}         S{}      溢写- (0/0)
3 插入 (c,7)     R{a:5,b:3,c:7}     S{}      溢写- (0/0)
4 插入 (d,2)     R{b:3,c:7,d:2}     S{a:5}   溢写a (1/0)
5 常驻更新 (b,4) R{b:7,c:7,d:2}     S{a:5}   溢写- (1/0)
6 回载合并 (a,1) R{a:6,b:7,d:2}     S{c:7}   溢写c (2/1)
7 回载合并 (c,2) R{a:6,b:7,c:9}     S{d:2}   溢写d (3/2)
8 回载合并 (d,5) R{a:6,c:9,d:7}     S{b:7}   溢写b (4/3)

(甲) 正确回载 a=5+1=6；若丢弃溢写部分和、只取新值，第6步 a=1，且 a 之后常驻到结束，最终 View a=1（应为6）。
(乙) 第4步正确溢写 a（访问序最早）；LRU 方向写反则溢写 c（最近插入者），此后第6步 a 变常驻命中、第8步不再溢写，总溢写次数 4 错成 2。
(丙) 回载不删溢写条目则 a 常驻6/溢写5 两处共存，按相加计 View 中 a=11（应为6），违反「至多一处」。

不变量的保证位置与钉住测试：
1 与批量重算一致：agg.(*Aggregator).apply 每事件只加一次，spill.(*Store).Evict/LoadIn 只搬运不改值 → api_test.TestViewMatchesBatch。
2 常驻数≤limit：插入/回载后 ResidentLen()>limit 立即 spill.(*Store).Evict 回到 ==limit → spill_test.TestResidentLimit（八步轨迹另由 api_test.TestEightStepTrace 逐步钉住）。
3 回载合并不丢不重：spill.(*Store).LoadIn 以「溢写部分和+新值」入常驻并删除溢写条目 → spill_test.TestReloadMerge、api_test.TestEightStepTrace（第6步 a=6）。
4 失败不留痕：agg.(*Aggregator).Feed 先影子累加整批校验（空键 ErrEmptyKey/溢出 ErrOverflow）通过才 apply；limit 由 api.New 拒（ErrInvalidLimit）→ api_test.TestRejectedBatchAtomic、api_test.TestSentinelErrors。
