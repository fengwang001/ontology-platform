# 区间合并器设计笔记

## 相接区间的归属推导（对应需求第三节）

- 左闭右开语义：`[1,2)` 含 1 不含 2，`[2,3)` 含 2。两者无公共点，但也无缝隙：
  不存在任何实数 x 满足 `2 <= x < 2`，即两区间之间插不进任何点。
- 合并的目标是「覆盖不变 + 互不相交的最简表示」。相接两段合并为 `[1,3)` 后，
  并集不变（`[1,2) ∪ [2,3) = [1,3)`），表示更简，且仍为左闭右开区间。
- 若相接不合并，`Ranges()` 会对同一段连续值域给出两种等价表示（`[[1,3)]` 与
  `[[1,2),[2,3)]`），违反「顺序无关 / 结果唯一」的收敛性——这正是线上事故根因。
- 结论：**相接即合并**。判定条件用 `a.end >= b.start`（重叠或相接都并入）；
  若误用 `a.end > b.start`，相接两段会被判为「不重叠」而拆开，违反需求第二节第 4 条。
- 测试钉住：`check` 包中 `TestTouchingMerge`（正向）与 `TestStrictGreaterIsWrong`（反向，
  内联 `>` 错误实现并断言其拆分相接区间）。

## 语义条目索引（需求第二节）

- 1 左闭右开/空区间：`iv/iv.go` `New`；测试 `TestValidation`（哨兵错误 `errors.Is` 可区分）。
- 2 互不相交且升序：`merge/merge.go` `Add` 归并循环 + `Ranges`；测试 `TestMergeCases`。
- 3 覆盖不变：对照 `check/check.go` `Naive.Ranges`；测试 `TestCoverageAndOrder`。
- 4 相接即合并：`merge/merge.go` 仅 `r.End < lo`、`hi < r.Start` 才留缝（等价 `a.end >= b.start` 并入）；
  测试 `TestMergeCases` 首条正向、`TestStrictGreaterIsWrong` 反向钉住 `>` 错误实现。
- 5 顺序无关：测试 `TestCoverageAndOrder`（同一批 shuffle 后结果相同）。
- 复杂度（第四节）：`merge/merge.go` 非导出计数器 `total` + `Total()`；测试 `TestMergeNeverGrows`。
- 并发（第五节）：`merge/merge.go` `sync.Mutex` 保护；测试 `TestConcurrentAdd`（16 goroutine，-race 干净）。
