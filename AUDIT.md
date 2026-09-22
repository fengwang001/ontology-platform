# 审计：不变量保证位置与测试对应

## 第二节五条不变量
1. 子树最大右端点正确
   - 保证：`node/node.go` 的 `(*Node).refresh`（由 `Rebalance`/旋转调用），
     插入回溯 `tree/tree.go:insert`、删除回溯 `deleteOne` 每次都经 `node.Rebalance`。
   - 钉住：`tree.TestInsertAndSelfCheck`、`tree.TestDeleteMultiset`、
     `tree.TestRandomMatchesNaive`（每轮调 `Tree.SelfCheck` 逐节点比对 MaxR）。
2. 查询不漏不多（== 朴素线性扫描）
   - 保证：`ival/interval.go:Overlaps`（交点集非空）；
     `tree/search.go:Stab/Overlap` 的中序遍历 + MaxR 剪枝（剪枝只丢弃
     被证明不相交的子树）。
   - 钉住：`tree.TestRandomMatchesNaive`（3 固定种子、插入/删除/查询混合，
     与朴素扫描逐项比对）；`query.TestRandomMatchesNaive`。
3. 结果顺序确定、与插入顺序无关
   - 保证：查询中序收集，键仅为 `(L,R)`，相等区间多重集不可区分。
   - 钉住：`query.TestOrderIndependent`（多种插入顺序逐位比对）。
4. 删除彻底；多重集只删一个
   - 保证：`tree/tree.go:deleteOne` 删除中序第一个相等节点，计数 -1。
   - 钉住：`tree.TestDeleteMultiset`、`query.TestDeleteSemantics`。
5. 空树与零长度
   - 保证：空树遍历 nil 返回空切片；`Overlaps`/`Contains` 对零长度恒假。
   - 钉住：`ival.TestContains`、`ival.TestRelations`、
     `query.TestZeroLength`、`tree.TestInsertAndSelfCheck/empty`。

## 自检方法
- `tree/check.go:Tree.SelfCheck`：核验 MaxR、BST 序、AVL 平衡、高度上限
  `3*bits.Len(n+1)+2`、节点总数 == 计数；错误为 `tree.ErrInvariant`。

## 第四节复杂度实测（`go test -v -run` 实测日志）
- N=1000 单点命中（`query.TestStabVisitSublinear/n1000`）：访问 **10** 个节点，断言 <=100。
- N=100000 单点命中（`query.TestStabVisitSublinear/n100000`）：访问 **17** 个节点，断言 <=100。
- N=100000、命中 M=500（`query.TestOverlapVisitProportionalToHits`）：
  访问 **518** 个节点（≈ M+树高，断言 <= 4M+100=2100，远小于 N）。
