# 时间序列间隙填充 NOTES

## 一、推导（step=10，键 k，逐点）

| 到达点 | 与上一点间的缺失时刻 | 各缺失时刻填充值(LOCF) | 本点之后「上一个点」 |
|---|---|---|---|
| (0,5)   | 无（首点之前不填充） | —      | (0,5)   |
| (30,8)  | 10, 20              | 5, 5   | (30,8)  |
| (40,8)  | 无（相邻，差=step）  | —      | (40,8)  |
| (70,12) | 50, 60              | 8, 8   | (70,12) |
| (100,12)| 80, 90              | 12, 12 | (100,12)|

- (甲) LOCF 对 TS=10、20 都填 5。若用固定常数 0 填充，TS=10 错成 0；0 < 前点值 5，破坏单调不减。
- (乙) 线性插值：TS=10 → 5+(8-5)*(10-0)/(30-0) = 6；TS=20 → 5+(8-5)*(20-0)/(30-0) = 7（正确均为 5）。
- (丙) 区间错成含右端点 (prevTS, TS] 时，第 2 点到达会把真实点 TS=30 用 prevVal 覆盖，错成 5（正确为 8）。

## 二、四条不变量 → 代码位置 → 钉住它的测试

1. 与朴素参照一致：`fill.Feed` 对每个真实点用 `grid.Gap` 枚举缺失时刻并以前值追加 → `TestViewMatchesNaive`。
2. 单调不减：`fill.Feed` 中填充值恒等于 prevVal，且逐点校验落在 `[prevVal, Val]` 闭区间 → `TestViewMonotone`（断言 TS 严格升序、Val 不减、真实点不被覆盖、填充值==前一真实值）。
3. 无遗漏无多余：`grid.Gap` 只枚举开区间 `(prevTS, TS)`，真实点本身随后追加，首点前/末点后不动 → `TestViewGridComplete`。
4. 失败不留痕：`fill.Feed` 两阶段（先对整批点全部校验，通过后才落状态）→ `fill.TestRejectedFeedNoTrace`。

复杂度：`fill.Filler.probes`（非导出）记录每次 Feed 定位键的哈希查找次数 → `fill.TestProbesIndependentOfSize`。
并发：`api` 用 `sync.RWMutex`，View/Last/SelfCheck 走读锁 → `TestConcurrentView`。
