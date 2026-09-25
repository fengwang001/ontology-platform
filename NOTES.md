# 双时间戳版本历史 — 推导与不变量

## 第三节推导：New(10)，同一 Key 依次 Apply A@Ev15/In1、B@Ev25/In2、C@Ev5/In3、D@Ev20/In4

| 步 | 到达版本 | LatestEvent（值@Ev） | LatestIngest（值@In） |
|---|---|---|---|
| 1 | A@Ev15,In1 | A@15 | A@In1 |
| 2 | B@Ev25,In2 | B@25（25>15，Ev 指针更新） | B@In2 |
| 3 | C@Ev5,In3 | B@25（5<25，指针不变） | C@In3 |
| 4 | D@Ev20,In4 | B@25（20<25，指针不变） | D@In4 |

- (甲) 若 LatestEvent 误实现为「返回最后到达的版本」：第 4 步后返回 **D**（Ev=20），漏掉了 Ev 更大的 **B@25**（正确应为 B）。
- (乙) 若 AtEvent 用 `Ev < T`（严格小于）：AtEvent(K,20) 只在 Ev∈{15,5} 中取最大，返回 **A@15**（正确应为 D@20，因 20≤20 应命中 D）。
- (丙) 若同 Ev 取 In 更小（先到）者：X@Ev10/In1、Y@Ev10/In2 中返回 **X**（正确应为 Y，规则要求同 Ev 取 In 最大者）。违反第二节**不变量 1**（与朴素扫描一致——定义即取 In 最大者）。

## 第二节四条不变量：保证位置与钉住测试

1. 与朴素扫描一致：`ver/ver.go` Append 的 Ev 指针维护 + 四个查询实现；测试 `ver.TestNaiveConsistency`、`api.TestSelfCheck`（SelfCheck 内置朴素比对）。
2. 事件时间最新单调：`ver/ver.go` Append 中仅当 `v.Ev >= 当前指针 Ev` 才更新指针；测试 `ver.TestEventLatestMonotonic`。
3. 摄取时间严格递增：`hist/hist.go` Apply 中 `in <= MaxIn()` 即拒绝；测试 `api.TestIngestStrictlyIncreasing`、`api.TestConcurrentApplyAndQuery`。
4. 失败不留痕：`hist/hist.go` Apply 先完成全部校验（EmptyKey/NonMonotonic/Capacity）再 Append，校验失败直接 return；测试 `api.TestFailedApplyNoSideEffect`、`api.TestRejections`。
