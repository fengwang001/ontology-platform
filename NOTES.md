# ontology-436 索引嵌套循环连接 NOTES

## 第三节推导：S=[2,3,3,3,5,7]（已升序），R=[3,5,1,3]

| r.Key | lo=第一个>=k | hi=第一个>k | 匹配区间 [lo,hi) | 本步匹配条数 |
|---|---|---|---|---|
| 3 | 1 | 4 | S[1:4]=[3,3,3] | 3 |
| 5 | 4 | 5 | S[4:5]=[5] | 1 |
| 1 | 0 | 0 | 空区间 | 0 |
| 3 | 1 | 4 | S[1:4]=[3,3,3] | 3 |

总输出 = 3+1+0+3 = **7 条**。

- **(甲)** 下界误为「第一个 S[i] > k」：key=3 时 lo=hi=4，区间错成 **[4,4)（空）**，匹配 0 条；凡存在于 S 的键区间都塌空，整表总输出 **0 条**（正确 7，漏 7）。
- **(乙)** 索引去重、每键只记首位置：key=3 只配 **1 条**；整表 1+1+0+1 = **3 条**（正确 7，漏 4，丢的是重复键的展开）。
- **(丙)** 单调前进游标不回退：扫过第一个 3 后游标停在 5 处，R 无序，到第二个 key=3 时游标已越过所有 3，匹配 **0 条**；整表 3+1+0+0 = **4 条**（正确 7，漏 3）。

## 四条不变量：保证位置 + 钉住测试

1. **与朴素参照一致**：`inj.Joiner.Probe` 对每条 r 独立调 `idx.Bounds` 展开 `[lo,hi)`，顺序与朴素双重循环相同；测试 `TestProbeMatchesNaive`（api_test，含多档随机输入），并被 `api.SelfCheck` 内置核验。
2. **探测区间正确**：`idx.Index.Bounds` 二分求 lo（第一个 >=k），自 lo 扫描求 hi（第一个 >k），值与定义精确一致；测试 `TestBoundsExact`（api_test，表驱动逐键核验区间内外），并被 `api.SelfCheck` 核验。
3. **索引有序且为 S 的排序**：`idx.Build` 先校验升序再整体复制，多重集与 S 相同；测试 `TestErrorsDistinguishable` 拒未升序，`api.SelfCheck` 逐位比对索引内容与 S。
4. **失败不留痕**：`idx.Build`/`inj.Probe` 先全量校验、全部通过后才落状态（`api.BuildIndex` 仅在 Build 成功后 `SetIndex`）；测试 `TestRejectLeavesStateUnchanged`，并被 `api.SelfCheck` 核验。

复杂度：`inj` 非导出计数器 `cmps` 只统计二分定位比较，`TestProbeCostLogarithmic`（inj 包内白盒）钉住 <= ceil(log2 m)+1；并发只读一致性由 `TestConcurrentProbeIdentical`（-race）钉住。
