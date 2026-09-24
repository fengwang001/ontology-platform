# ontology-396 半朴素传递闭包 NOTES

## 推导表（边集 a→b, b→c, c→d, b→d, d→e, e→b；环 b→c→d→e→b）

| 轮 | 本轮 Delta | join 候选数 | 新增数 | 去重丢弃的候选 | 本轮后 path 大小 |
|---|---|---|---|---|---|
| 0 | ab bc cd bd de eb | 0（基例，无 join） | 6 | — | 6 |
| 1 | ac ad ce be db ec ed | 8 | 7 | bd | 13 |
| 2 | ae cb bb dc dd ee | 8 | 6 | ad ed | 19 |
| 3 | cc | 8 | 1 | ab bc bd cd dd de eb | 20 |
| 4 | ∅（不动点，停） | 1 | 0 | cd | 20 |

- **(甲)** 第 2 轮半朴素用 Delta₁（7 元组）join 得 **8** 个候选；若退化成用全量 path₁（13 元组）join 则得 **16** 个候选。多出的 8 个是对第 0 轮 6 条边的重复再推导，例如 **(a,c)**（ab∘bc）与 **(b,d)**（bc∘cd）——它们第 1 轮已在 path 中。
- **(乙)** 最终 path 共 **20** 个元组；自身到自身（环带来的自环）为 **bb cc dd ee** 四个（a 不在环上，无 aa）。若错误地只跑 2 轮就停，会漏掉第 3 轮才导出的 **(c,c)**（路径 c→d→e→b→c，长 4 条边），path 错成 **19**。
- **(丙)** a→d 的两条导出路径：① 经中间结点 **b**，第 **1** 轮 ab∘bd；② 经中间结点 **c**，第 **2** 轮 ac∘cd。第 2 轮候选 ad 已在 path 中，被去重丢弃，不进 Delta₂。若不去重（path 当多重集、逢导出必追加）：环 b→c→d→e→b 上的元组每轮都被重新导出、反复追加，Delta 永不为空，**永远到不了不动点，求值不终止**。

## 四条不变量：代码位置与钉住它的测试

1. **与朴素闭包一致**：`api.SelfCheck` 内以 BFS 可达性逐元组对照（api/api.go）；测试 `TestMatchesNaiveClosure`（api/api_test.go）。
2. **不动点正确（终止且末轮 Delta 为空）**：`semi.Engine.Eval` 的 `for len(delta) > 0` 循环（semi/semi.go）；测试 `TestFixpointLastDeltaEmpty`（semi/semi_test.go）。
3. **每个导出元组恰好进 Delta 一次**：`semi` 每轮候选用 `path.Add` 返回值抑制重复（semi/semi.go）；测试 `TestDeltaExactlyOnce`（semi/semi_test.go）。
4. **失败不留痕**：`api.New`/`AddEdge` 先 `check` 校验、通过后才改边集与求值缓存（api/api.go）；测试 `TestRejectLeavesStateUnchanged`。

复杂度：链图 m 条边时 join 候选总数恰为 m(m−1)/2，由 `semi` 非导出计数器记录，测试 `TestCandidateCounterChain`（semi/semi_test.go，包内测试读取非导出字段）。
