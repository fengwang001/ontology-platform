# CEP `A -> B within T` 推导（T=5，maxPending=8）

| # | 事件 | 宽松后 k 的待匹配 A 队列 | 宽松输出 | 严格输出 |
|---|---|---|---|---|
| 1 | k:A@1 | [1] | 无 | 无 |
| 2 | k:C@2 | [1] | 无 | 无 |
| 3 | k:B@3 | [] | (A@1,B@3) | 无（上一事件是 C） |
| 4 | k:A@4 | [4] | 无 | 无 |
| 5 | k:A@6 | [4,6] | 无 | 无 |
| 6 | k:B@9 | [6] | (A@4,B@9) | (A@6,B@9) |
| 7 | k:B@11 | [] | (A@6,B@11) | 无（上一事件是 B） |
| 8 | k:A@12 | [12] | 无 | 无 |
| 9 | z:A@14 | [12]（z 不影响 k） | 无 | 无 |
| 10 | k:B@17 | [] | (A@12,B@17) | (A@12,B@17) |

(甲) 宽松最终：(A1,B3)(A4,B9)(A6,B11)(A12,B17)；严格最终：(A6,B9)(A12,B17)。先滤掉无关事件再判连续会**多出 (A1,B3)**（C@2 被忽略后 B3 紧接 A1）；按全局上一事件判连续会**少掉 (A12,B17)**（B17 的全局上一事件是 z:A14，跨 Key）。
(乙) 开区间 `<T`、过期 `>=T`：B9 时 A4 差 5 先被过期移除，宽松配 A6；B17 时 A12 差 5 被移除。宽松最终 (A1,B3)(A6,B9)，严格最终 (A6,B9)。
(丙) A 不消费且每个 B 与全部窗口内 A 配对：(A1,B3)(A4,B9)(A6,B9)(A6,B11)(A12,B17)。**A6 被重复配给 B9、B11**（B9 也同时配了 A4、A6），违反不变量 3（不重复）。

不变量保证位置 / 钉住测试：
1. 与朴素一致：`cepmatch.step` 队首即最早到达未消费 A、严格只看同 Key 上一事件；`api.naive` 全表回看对照。测试 `TestNaiveEquivalence`、`TestTenEvents`。
2. 匹配合法：`cepwin.InWindow`（0<=差<=T 闭区间）、按 Key 分 map、到达顺序先入队。测试 `TestWindowEdge`、`TestNaiveEquivalence`。
3. 不重复：宽松配对即出队消费、严格 last 每事件覆盖。测试 `TestTenEvents`、`TestNaiveEquivalence`（唯一性断言）。
4. 失败不留痕：`cepmatch.Engine.Feed` 入口 snapshot、出错 restore，整批回滚。测试 `TestBatchAtomic`、`TestSentinelErrors`。
