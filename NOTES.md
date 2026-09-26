# 传递闭包推导 n=5：边 0→1, 1→2, 2→0, 2→3, 3→4（对角初值 false）

逐 k 新增可达对（本步前不可达、本步后可达）：
- k=0：(2,1)（2→0→1）
- k=1：(0,2)（0→1→2）、(2,2)（2→1→2，环闭合）
- k=2：(0,0)、(0,3)、(1,0)、(1,1)、(1,3)
- k=3：(0,4)、(1,4)、(2,4)（各接 3→4）
- k=4：无（4 无出边）

闭包矩阵（行 i 列 j，0..4）：

```text
11111
11111
11111
00001
00000
```

- 甲 自反过度（空路径也算可达）：Reach(4,4) 错判 true；正确 false——4 不在环上，无 ≥1 边回路。
- 乙 只算跳数 ≤2：Reach(0,4) 错判 false（实路径 0→1→2→3→4 长 4）；正确 true。
- 丙 跳过对角线更新：Reach(2,2) 错判 false（环 2→0→1→2 长 3 无法回写）；正确 true。

不变量 → 代码保证位置 → 钉住测试：
1. 定义正确（≥1 边；环上 Reach(i,i)=true）：tc.go Compute 对角与普通格同规则更新、New 中对角置 false — TestSpecGraphClosure
2. 与朴素 BFS 参照逐格一致：tc.go New 快照直接边 + Compute 三重循环 — TestNaiveReference
3. 传递性：闭包矩阵对全三元组成立，Compute 三重循环 — TestTransitivity
4. 失败不留痕（四类拒绝互不相同、状态不变）：dg.go AddEdge 先全部校验后写矩阵 — TestRejectNoTrace
另：api.go SelfCheck 对内置图复核四条由 TestSelfCheck 钉住；O(1) 查询（非导出 lastChecks）由 TestQueryCounterConstant 白盒直读钉住。
