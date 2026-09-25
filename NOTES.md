# NOTES

## 七步推导（K=2，全序：分降序、同分 id 升序）

| 步 | 操作 | 现存项（分数:id 全序） | 榜内 top2 | 落榜集 |
|---|---|---|---|---|
| 1 | Add a,10 | `10:a` | `[a]` | `{}` |
| 2 | Add b,20 | `20:b 10:a` | `[b,a]` | `{}` |
| 3 | Add c,15 | `20:b 15:c 10:a` | `[b,c]` | `{a}` |
| 4 | Add d,15 | `20:b 15:c 15:d 10:a` | `[b,c]` | `{d,a}` |
| 5 | Add e,20 | `20:b 20:e 15:c 15:d 10:a` | `[b,e]` | `{c,d,a}` |
| 6 | Remove b | `20:e 15:c 15:d 10:a` | `[e,c]` | `{d,a}` |
| 7 | Remove e | `15:c 15:d 10:a` | `[c,d]` | `{a}` |

- **(甲) 并列时"新到优先"错规**：第 4 步 d 顶掉 c，榜错成 `[b,d]`（c 落榜）；第 5 步 e 顶掉同分的 b，榜错成 `[e,b]`。正确值分别为 `[b,c]`、`[b,e]`。
- **(乙) 落榜即丢**：第 3 步丢 a、第 4 步丢 d、第 5 步 c 落榜即被丢；第 6 步删 b 后无项可补，榜错成 `[e]`（只有 1 个），补不上应为第 K 名的 c，正确是 `[e,c]`。
- **(丙) 重复 Add 当更新**：第 3 步后 `Add(g,c,999)` 把 c 改成 999，榜错成 `[c,b]`；正确为报 `ErrDuplicate`、榜仍 `[b,c]`、落榜仍 `{a}`。

## 四条不变量：保证位置 / 钉住的测试

1. 与批量重算一致：`topk/topk.go` 的 `Add/Remove` 只在榜满时与堆顶末名比较一次、溢出项进有序 `below`，删榜内即取 `below[0]` 补位；`groups.Manager` 按 key 隔离。测试 `TestBatchRecompute`（随机增删序列对拍暴力重算）。
2. 全序且稳定：`topk/topk.go` 的 `less`（分降、id 升序，严格全序），榜内堆与落榜切片同一序。测试 `TestSevenSteps`（上表七行）。
3. 补位唯一：`topk.Set.Remove` 中删榜内项后提升的恰是 `below[0]`（落榜全序首位＝剩余第 K 名）；删落榜项不碰榜。测试 `TestPromotion`。
4. 失败不留痕：`topk.Add/Remove` 先查 `score` 映射再做任何变更；`api` 先校验空串/k 再下发，四类哨兵错误互异。测试 `TestRejectedOpsNoTrace`、`TestFaultsDistinct`。

复杂度计数器 `Set.lastCheck`（非导出，仅同包测试可读）：`TestAdmissionCheckBounded`（m=100/1000/10000，断言 ≤2）。并发：`api` 单一 RWMutex 包裹，`TestConcurrentTopK`；自检入口 `api.SelfCheck`。
