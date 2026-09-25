# latest-per-key 推导（R=5，窗口 [0,10)，10 条 TS 均在窗内）
|步|Key|喂后该Key存活候选(V,TS,Del)|判定|
|1|a|(1,1,put)|新存活|
|2|b|(10,2,put)|新存活|
|3|a|(2,4,put)|新存活，(1,1)折叠|
|4|c|(5,3,put)|新存活|
|5|b|(0,5,tomb)|新存活，(10,2)折叠|
|6|a|(0,6,tomb)|新存活，(2,4)折叠|
|7|c|(0,7,tomb)|新存活，(5,3)折叠|
|8|a|(3,8,put)|新存活，(0,6)折叠|
|9|d|(7,6,put)|新存活|
|10|d|(7,6,put)不变|本条(9,2,put)被折叠丢弃|
保留期：a(3,8,put)保留；b(0,5,tomb) 5+5=10<=10 丢弃→无输出；c(0,7,tomb) 12>10 保留；d(7,6,put)保留。
输出(Key字典序)：a{3,8,F}、c{0,7,T}、d{7,6,F}；b 不出现。
甲：错把“最后到达”当最新→d 取第10步(9,2,put)，输出 Value 错成 9（正解 7）。
乙：丢弃条件写成严格 < 则 10<10 不成立→b 被错保留，输出 b{0,5,T}（正解丢弃、无输出）。
丙：墓碑丢弃后更早 put 不得复活；错实现会输出 b{10,2,F}，正解 b 无任何输出。

## 不变量（代码保证位置 / 钉住测试）
1 与朴素重算一致：compact.Compact 窗内按 max-TS 取存活、rec.Drop 判墓碑保留期 / TestNaiveEquivalence
2 无重复 Key：map[key][]rec 分组、按排序后的键收集至多一条 / TestNoDuplicateKeys
3 不复活：只输出存活者，存活墓碑被 Drop 则 continue、更早记录已折叠 / TestNoResurrection
4 失败不留痕：New/Feed/Compact 先完成全部校验再改状态，非法批次整体拒绝 / TestRejectedOpsLeaveNoTrace
另：折叠比较计数为非导出字段 compact.cmps，同包白盒 TestFoldComparisonsBounded 钉 O(1)；并发一致性由 TestConcurrentCompact 钉住。
