# NULL 分组语义下的增量计数视图 — NOTES

## 第三节：八步推导（maxGroupLen=16，计数 0 的组记「不存在」）

| 步 | 事件 | NULL 组 | 空串组 | "a" 组 | "b" 组 | 存在的组 |
|---|---|---|---|---|---|---|
| 1 | (∅,+2) | 2 | 0 | 0 | 0 | {NULL} |
| 2 | ("",+1) | 2 | 1 | 0 | 0 | {NULL, ""} |
| 3 | ("a",+3) | 2 | 1 | 3 | 0 | {NULL, "", a} |
| 4 | (∅,+1) | 3 | 1 | 3 | 0 | {NULL, "", a} |
| 5 | ("b",+5) | 3 | 1 | 3 | 5 | {NULL, "", a, b} |
| 6 | ("",-1) | 3 | 0 | 3 | 5 | {NULL, a, b} |
| 7 | ("a",-2) | 3 | 0 | 1 | 5 | {NULL, a, b} |
| 8 | (∅,-3) | 0 | 0 | 1 | 5 | {a, b} |

- **(甲)** 第 4 步后 NULL 组=3、空串组=1。若错误地把 `nil` 与 `""` 合并为同一键：合并组累计 +2+1+1=4，故 `Count(nil)` 错成 **4**（应为 3），`Count(&"")` 错成 **4**（应为 1）。
- **(乙)** 第 6 步空串组计数降到 0，按规则立即**不存在**。若实现保留零值组，第 6 步后 `Groups()` 会多列出**空串组、值为 0**。
- **(丙)** 第 8 步后 NULL 组已移除；再 `Feed` 一个 `(∅,-1)` 会使 NULL 组计数变负，正确行为：整批拒绝、返回哨兵错误 `ErrNegative`、**不留任何痕迹**（NULL 组仍不存在，`Count(nil)` 仍为 0，其余组不变）。若实现允许负计数，`Count(nil)` 会错成 **-1**。

## 第二节：四条不变量的落点与钉住它们的测试

1. **与批量重算一致**：`agg.Map.ApplyBatch` 按归一化键累加、归零即 `delete`（agg/agg.go）；测试 `TestBatchEquivalence`（api/api_test.go，多种规模+随机顺序对比朴素模型）。
2. **NULL 与空串不混**：`grp.Of` 把键归一为三分态（Null/Empty/Str）可比较结构体（grp/grp.go）；测试 `TestNullVsEmpty`。
3. **计数非负且归零即删**：`ApplyBatch` 先整体校验，任一键新值为负返回 `ErrNegative` 且不改状态，为 0 删除键；测试 `TestNonNegativeZeroRemoval`。
4. **失败不留痕**：`api.Feed` 两阶段——先聚合校验整批（长度、负值），全部通过才应用（api/api.go）；测试 `TestFailureAtomicity`。

复杂度：`agg.Map.locate` 每次定位只查 1 个组并存入非导出字段 `lastChecks`；测试 `TestLocateChecksBounded`（agg/agg_test.go，包内白盒）断言其不随 m 增长。并发只读一致性由 `TestConcurrentReads` 钉住。
