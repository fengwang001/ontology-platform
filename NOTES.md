# NOTES

## 懒标记下推时机推导（第三节）

懒标记 `lazy[i]` 的含义：节点 i 代表的整段区间已被整体加过 delta，该 delta
只记进了 `sum[i]`，尚未传给子节点——「我欠子节点一个 delta」。因此，任何
操作只要需要**进入某个节点的子节点**，就必须先 `pushdown` 把这笔债还掉，
否则子节点的 `sum` 是不含祖先 delta 的陈旧值。

- 更新时：AddRange 部分覆盖当前节点、要递归进子节点前，先 pushdown。
- 查询时：SumRange 部分覆盖当前节点、要递归进子节点前，同样必须先
  pushdown。查询只读 sum 字段，但子节点的 sum 是否「新」取决于祖先是否
  下推过，与查询本身修不修改树无关。

为什么只在更新时 pushdown 不够：反例 `AddRange(0, n-1, 1)`。根节点被完全
覆盖，只更新 `sum[root]` 并打上 lazy，不递归、不下推。随后 `SumRange(0,0)`
从根向下走，路径上内部节点 lazy 非零而子节点 sum 全为 0；若查询不
pushdown，递归到叶子读到 0，结果偏小（正确值为 1）。查询命中的正是这些
「未下推的中间节点」，更新时的 pushdown 管不到它们。

测试钉住：`TestQueryMustPushdown` 断言正确实现 `SumRange(0,0)==1`；同测试
内联一份「只在更新时 pushdown、查询不 pushdown」的错误实现 buggy，断言其
`SumRange(0,0)==0`（陈旧值）。

## 语义与代码位置（第二节）

1/2/3/5. 区间加、区间求和、混合、边界（n=1 与 l==r）：`segtree.Tree.AddRange`/`SumRange`（segtree/segtree.go），测试 `TestAgainstNaive`（对照 `check.Naive`）。
4. `ErrBadRange`：哨兵错误定义在 segtree，seg 包再导出，测试 `TestBadRange`（errors.Is）。
复杂度：`Tree.visited` 计数，`TestAgainstNaive` 内断言 ≤ 4·log2(n)+8（含 n=100000）。并发：`TestConcurrentSum`（16 goroutine 只读）。第三节反例 buggy 在 check/check.go，由 `TestQueryMustPushdown` 钉住。
