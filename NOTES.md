# NOTES — 增量 delta 编码存储

## 第三节：十行分步表（初值全 0；`—` 表示该步非读取）

| # | 操作 | base(a,b) | delta(a) | delta(b) | Get 结果 | DeltaCount |
|---|------|-----------|----------|----------|----------|------------|
| 1 | Apply(a,+10) | 0,0 | [10] | [] | — | 1 |
| 2 | Apply(b,+20) | 0,0 | [10] | [20] | — | 2 |
| 3 | Apply(a,−4) | 0,0 | [10,−4] | [20] | — | 3 |
| 4 | Get(a) | 0,0 | [10,−4] | [20] | a=6 | 3 |
| 5 | Compact() | 6,20 | [] | [] | — | 0 |
| 6 | Apply(a,+7) | 6,20 | [7] | [] | — | 1 |
| 7 | Apply(b,−5) | 6,20 | [7] | [−5] | — | 2 |
| 8 | Get(b) | 6,20 | [7] | [−5] | b=15 | 2 |
| 9 | Compact() | 13,15 | [] | [] | — | 0 |
| 10 | Get(a),Get(b) | 13,15 | [] | [] | a=13, b=15 | 0 |

- **(甲)** 正确 `Get(a)=6`（10−4）。按绝对值累加错成 **14**；丢弃/钳制负 delta 错成 **10**。
- **(乙)** 正确 `Get(a)=13`、`Get(b)=15`。若 `Compact` 误为 `base=Σ(delta)` 覆盖：base 变 a=7、b=−5，`Get(a)` 错成 **7**、`Get(b)` 错成 **−5**。
- **(丙)** 正确 `Get(b)=15`。若 `Get` 只返回 base 错成 **20**；只返回 Σ(delta) 错成 **−5**。

## 第二节：四条不变量的保证位置与钉住测试

1. **与朴素参照一致**：`delta.Entry.Sum`（base+按序累加）与 `Store.Compact`（`base+=Σ` 后清空）共同保证；测试 `TestNaiveModelConsistency`（随机交错序列对拍朴素模型）。
2. **Compaction 保值**：`store.Store.Compact` 只重写内部表示不动可见值；测试 `TestCompactPreservesValues`（Compact 前后逐 Key 对拍）。
3. **追加正确性**：`delta.Entry.Append` 纯尾部追加、`Store.Apply` 只触碰目标 Key；测试 `TestApplyDeltaEffect`（含负/零 delta，校验目标增量与其他 Key 不变）。
4. **失败不留痕**：`api.New`/`Store.Apply` 先校验后变更，任一拒绝路径在写状态之前返回；测试 `TestRejectionLeavesNoTrace`（三类错误各自拒绝后 base/delta/DeltaCount 不变且可继续用）。
