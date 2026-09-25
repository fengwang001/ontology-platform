# 双时间戳版本历史 NOTES

## 第三节推导：New(10)，同一 Key 依次 Apply A@Ev15/In1, B@Ev25/In2, C@Ev5/In3, D@Ev20/In4

| 步 | 到达版本 | LatestEvent(值@Ev) | LatestIngest(值@In) |
|---|---|---|---|
| 1 | A@Ev15,In1 | A@15（唯一版本） | A@In1 |
| 2 | B@Ev25,In2 | B@25（25>15，指针前移） | B@In2 |
| 3 | C@Ev5,In3 | B@25（5<25，指针不动） | C@In3 |
| 4 | D@Ev20,In4 | B@25（20<25，指针不动） | D@In4 |

- (甲) 若把「摄取最新」当「事件最新」，第 4 步后返回 **D@20**（正确应为 B@25）；它漏掉了 Ev 更大的 **B（Ev=25 > 20）**，因为 C、D 后到但 Ev 更小，指针被错误地跟着到达顺序走。
- (乙) 若 `AtEvent` 用 `Ev < T`（严格小于），`AtEvent(K,20)` 只在 Ev∈{15,5} 中取最大，返回 **A@15**（正确应为 D@20，因为 20≤20 应命中 D）。
- (丙) 同 Ev 取 In 更小者：Apply X@Ev10/In1、Y@Ev10/In2 后返回 **X**（正确应为 Y，规则是 Ev 相同取 In 最大）。对应不变量 1（与朴素扫描一致）：朴素扫描按定义「Ev 最大、并列取 In 最大」必得 Y，错误实现与之不符。

## 四条不变量的保证位置与钉住测试

1. 与朴素扫描一致：ver.go 中 LatestEvent/LatestIngest/AtEvent/AtIngest 按定义实现（LatestEvent 由 Append 时维护的 maxEv 指针返回，指针更新规则与定义等价）；测试 `TestMatchesNaiveScan`（ver 包内，随机乱序序列对拍朴素扫描）。
2. LatestEvent 的 Ev 单调不减：ver.go `Append` 仅当 `v.Ev >= 当前最大 Ev` 才移动 maxEv 指针；测试 `TestLatestEventMonotonic`。
3. In 严格递增：hist.go `Apply` 在持锁下校验 `in > 该 key 当前最大 In`，拒绝返回 ErrNonMonotonicIngest；测试 `TestFailuresDistinctAndNoTrace`。
4. 失败不留痕：hist.go `Apply` 全部校验（空 key、In 单调、容量）先于任何写入，api.go `New` 先校验 maxVersions；测试 `TestFailuresDistinctAndNoTrace` 断言拒绝前后各查询结果不变。

复杂度：ver.go 非导出字段 `checked` 记录最近一次 LatestEvent 检查版本数（恒为 1，指针 O(1)）；测试 `TestLatestEventIsO1`（m=100/1000/10000 断言不随 m 增长），计数器只经包内测试与 ver.SelfCheck 内部读取，不出现在公开接口。
