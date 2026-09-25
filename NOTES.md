# 双流水位对齐器 — 推导与不变量

八步（w 为各流迄今最大 TS；未见 = -∞；W=min(wA,wB)；发出口序 (TS, 到达序)）：
|步|事件|wA|wB|W|判定|本步发出|步末滞留|
|1|A1|1|-∞|-∞|接受缓冲|无|A1|
|2|A2|2|-∞|-∞|接受缓冲|无|A1,A2|
|3|B1|2|1|1|接受发出|A1,B1|A2|
|4|A3|3|1|1|接受缓冲|无|A2,A3|
|5|B2|3|2|2|接受发出|A2,B2|A3|
|6|B5|3|5|3|接受发出|A3|B5|
|7|A4|4|5|4|接受发出|A4|B5|
|8|A2|4|5|3|迟到丢弃|无|B5|
（Close 把水位推 +∞，发 B5；总输出 A1,B1,A2,B2,A3,A4,B5。）

(甲) 第6步 W=3，只发 A3，B5 滞留。若 W=max：W=5，A3 与 B5 同发；B5(TS5) 抢在第7步 A4(TS4) 之前输出，出现 5 后接 4，非降断裂——慢流 A 未担保前快流不得超车。
(乙) 第1步 W=-∞，A1 只能缓冲。若未见当 +∞：W=1，A1 首步即被发出；此后 B 的首条 B0 会让输出变成 TS1 后接 TS0，非降断裂。若当成 0：W 错成 0；任一流首条 TS0 会在另一流从未见事件时被提前发出，“未观测”被混同为“已观测 0”。
(丙) A2 的 TS=2 < wA=4，迟到丢弃。若缓冲它，Close 尾部变成 …A4, A2, B5：4 后接 2 非降断裂，且比朴素结果多出 A(TS2) 这一条。

不变量保证位置 / 钉住测试：
1 朴素重排一致：align.go drain 按 (TS,Seq) 最小堆弹出、迟到事件不插入，api.go Feed/Close 累积 emitted —— TestNaiveReorder。
2 顺序非降：堆序 (TS,Seq) 且仅弹 TS<=W，Close 以 +∞ 全弹 —— align.go drain —— TestNonDecreasing。
3 不提前发出：任一流 unseen 时 alignedW 不给出有限 W，drain 一个事件都不弹 —— align.go alignedW/drain —— TestNoEarlyEmission。
4 失败不留痕：api.go Feed/Close 持锁先完成全部校验（哨兵 ErrBadStream/ErrNegativeTS/ErrClosed），通过后才改状态 —— TestRejectedLeavesNoTrace。
复杂度：align.go 非导出 cmpProbe 记“最近一次 drain 为定位最小 TS 而检视过的缓冲事件条数”；堆根即最小候选，每次定位只检视堆顶 1 条（与缓冲大小无关的常数）；白盒 TestHeapComparisonBound 断言 m=100…10000 时该数不随 m 增长（线性扫描要检视 m 条）。
