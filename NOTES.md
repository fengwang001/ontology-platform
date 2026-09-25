# NOTES

## 懒标记下推时机推导

不变量：`sum[i]` 对节点 i 自身永远是最新的（更新覆盖到 i 时立即结算），
`lazy[i]` 表示「i 的子节点还欠一个未结算的 delta」，即子节点的 `sum` 是陈旧的。

- 更新时下推：部分覆盖要继续向子节点递归，递归前必须把 `lazy[i]` 结算给
  子节点，否则子节点在旧值上累加，丢失祖先欠下的 delta。
- 查询同样要下推：查询在部分覆盖时也要进入子节点读 `sum`。若不在进入前
  pushdown，读到的子节点 `sum` 不含祖先欠下的 delta，结果偏小。
- 为什么只在更新时 pushdown 不够：一次全覆盖更新（如 `AddRange(0,n-1,1)`）
  只结算到根，根的子树全部挂着未下推的 lazy。此后若没有任何更新触达这些
  子树，查询就是第一个进入它们的人；查询不 pushdown，则命中中间节点时读到
  陈旧和。钉住：`AddRange(0,n-1,1)` 后 `SumRange(0,0)`，正确实现得 1，
  「查询不 pushdown」的错误实现得 0。
- 推论：pushdown 是只读路径上的写操作，故读也需互斥（见「并发」）。

## 语义条目位置

- 区间加：`segtree/segtree.go` `AddRange`/`add`；测试 `TestMixedOps`
- 区间求和：`SumRange`/`query`（进入子节点前 pushdown）；测试 `TestMixedOps`、`TestQueryPushdown`
- 混合一致：`check/check_test.go` 各测试对照 `check.Naive`
- 错误：`ErrBadRange`/`ErrOutOfBounds`/`ErrReversed`（`run` 校验，`errors.Is` 可分）；测试 `TestBadRange`
- 边界：单点见 `TestMixedOps` 首个用例；越界/l>r 见 `TestBadRange`
- 复杂度：非导出计数经 `LastVisited` 暴露；测试 `TestVisitBound`（≤4·log2(n)+8）
- 并发：`sync.Mutex` 互斥（查询 pushdown 也是写）；测试 `TestConcurrentReads`，`-race` 干净
