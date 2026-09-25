# NOTES — ontology-454 谓词下推（≤30 行）

链 F1→S→F2；S.max 初值 -∞（实现取 math.MinInt64）。

|Seq|Val/Kind|F1|max 前→后|S 通过|F2|输出|
|---|---|---|---|---|---|---|
|1|10/A|✓|−∞→10|✓|✓|Seq1(10)|
|2|25/A|✗(奇)|10→10|未到|未到|无|
|3|18/A|✓|10→18|✓|✓|Seq3(18)|
|4|30/B|✓|18→30|✓|✗|无|
|5|22/A|✓|30→30|✗|未到|无|
|6|40/A|✓|30→40|✓|✓|Seq6(40)|

正确输出：10,18,40；第 4 步后 max=30。

(甲) F1→F2→S：输出 10,18,22,40；多出 **Val=22**——事件 4 被 F2 挡在 S 外、max 停留 18，22 反成新高。
(乙) S→F1→F2：输出 10,40；丢失 **Val=18**——奇数 25 越过被下移的 F1 抵达 S，把 max 抬到 25，18 不再是新高，即被 Val=25 顶掉。
(丙) p→S：第 4 步 p 因 Kind=B 失败，30 不到达 S，**max 错误值=18**；正确值=30；差 12，源于 S「无论通过与否都更新 max」，F2 合到 S 上游后事件 4 被整条短路。

不变量 → 代码位置 / 钉住测试：

1. 与朴素链一致：`pipe` 的 `Run`（合并计划）对照 `RunNaive`（原序）；TestNaiveEquivalence。
2. 段内可交换/AND 合并：`Build` 单遍归并同段无状态过滤器为 `pred.And`（纯合取，可交换）；TestSegmentReorderMerge。
3. 屏障不可穿越：`Build` 校验 moves，闭区间内含 stateful 即返回 `ErrBarrier`，且禁止移动 stateful；TestBarrierRejected。
4. 失败不留痕：`Build` 先全部校验再构造计划；`Feed` 先整批校验事件、通过后才求值；TestRejectedLeavesNoTrace。
   并发只读：`api` RWMutex 保护 + 结果拷贝；TestConcurrentReaders。自检四不变量：TestSelfCheck。
