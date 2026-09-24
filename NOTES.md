# ontology-275：DRed 增量传递闭包 —— 推导与不变量

记号：`XY` 表示可达对 (X,Y)；边栏只列重数>0 的边，如 `BC2` 表示 B→C 重数为 2。

## 十步推导（New(10)）

| 步 | 操作 | 边(重数) | 本步之后的 R（逐对） | |R| | 过删 | 再推 |
|---|---|---|---|---|---|---|
| 1 | +A→B | AB1 | AB | 1 | — | — |
| 2 | +B→C | AB1 BC1 | AB BC AC | 3 | — | — |
| 3 | +A→C | AB1 BC1 AC1 | AB BC AC | 3 | — | — |
| 4 | +C→A | AB1 BC1 AC1 CA1 | AA AB AC BA BB BC CA CB CC | 9 | — | — |
| 5 | +B→C | BC2，余同步4 | 同步4 | 9 | — | — |
| 6 | −B→C | BC1，余同步4 | 同步4 | 9 | 0 | 0 |
| 7 | −A→B | BC1 AC AC CA | AA AC BA BC CA CC | 6 | 9 | 6 |
| 8 | +C→D | 步7 + CD1 | 步7 + AD BD CD | 9 | — | — |
| 9 | −C→A | BC AC CD | AC AD BC BD CD | 5 | 9 | 5 |
| 10 | −B→C | AC CD | AC AD CD | 3 | 2 | 0 |

(甲) 步7：过删 9、再推 6，R={AA,AC,BA,BC,CA,CC}。只过删不再推 ⇒ R=∅（9 对全丢）。只删 (A,B) 一对 ⇒ 多出 (B,B)、(C,B)（B 已无任何入边，这两对不再成立）。
(乙) 步9：过删 9、再推 5，R={AC,AD,BC,BD,CD}。只删 (C,A) 一对 ⇒ 多出 (A,A)、(B,A)、(C,C)；其中自到自的是 (A,A)、(C,C)：它们只靠环 A→C→A 支撑，删掉 C→A 后环断，A、C 各自再也无法经长度≥1 的路径回到自己。
(丙) 边当集合（步5被忽略）：步6 会把 B→C 删没，R 错成 {AA,AB,AC,CA,CB,CC}，少了 (B,A)、(B,B)、(B,C) 三对。步10 在正确实现下：过删 2（即 (B,C)、(B,D) 两对），再推 0。

## 四条不变量的落实位置与钉住测试

1. 与朴素 BFS 一致：`closure.Closure.AddEdge/RemoveEdge` 的并入与 DRed ⇒ `TestTenSteps`、`TestRandomVsNaive`。
2. 重数语义：仅当重数跨越 0↔1 才动 R（`api.AddEdge/RemoveEdge` 里看 `graph.Add/Remove` 的返回值）⇒ `TestMultiplicity`。
3. 单向变化：AddEdge 只并入不删、RemoveEdge 净减=过删−再推（`closure.AddEdge/RemoveEdge`）⇒ `TestMonotone`。
4. 失败不留痕：`api.AddEdge/RemoveEdge` 先校验后变更，四个互不相同哨兵错误 ⇒ `TestErrors`。
