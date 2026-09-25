# NOTES

M=3, maxFanIn=2。
R run 结构：run0=[1,3,5]，run1=[2,3,7]；多路归并（并列取编号小者）R'=[1,2,3(run0),3(run1),5,7]。
S run 结构：run0=[3,3,6]，run1=[2]；归并 S'=[2,3,3,6]。
六行表（R游标键, S游标键, 比较, 动作, 本步输出）：
1. 1, 2, <, 推进R, 无
2. 2, 2, =, 整段笛卡尔积, (2,2)×1
3. 3, 3, =, 整段笛卡尔积, (3,3)×4
4. 5, 6, <, 推进R, 无
5. 7, 6, >, 推进S, 无
6. 7, S耗尽, —, 结束, 无
正确总输出 = 1+4 = 5 条。
(甲) 第3步只输出 1 条（两游标各进一格后再次 3=3 又配 1 条），整表总输出 3 条。
(乙) 相等也推进 R，永不配对，整表总输出 0 条。
(丙) R 缺 Key 2、3、7，仅剩 [1,3,5]；连接只剩 (3,3)×2，5 条错成 2 条。

不变量 → 代码保证位置 → 钉住的测试函数：
1. 与朴素参照一致：mrg.go 的 join 相等键整段笛卡尔积；api.go Join 委托它 → TestJoinMatchesNaive（api/api_test.go）。
2. 归并有序：run.go MakeRuns 块内排序；mrg.go 堆序 (key, run编号) 与 node.next 弹出 → TestMergeSorted（mrg/mrg_test.go）。
3. run 结构：run.go MakeRuns 满 M 切块、末块 ≤M、块内升序 → TestRunStructure（mrg/mrg_test.go）。
4. 失败不留痕：api.go New 直接拒建；BuildR/BuildS 先全量校验、构造新切片后才整体换写 → TestRejectionLeavesState（api/api_test.go）。
复杂度计数器 probe（非导出）在 mrg.go node.sift 中累加 → TestProbeCountLogarithmic（mrg 包内白盒）。
并发只读：api.go RWMutex，Join 每次返回全新切片 → TestConcurrentJoin（api/api_test.go）。
