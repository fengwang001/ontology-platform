# NOTES — 三维 CUBE 增量维护

## 第三节：五步推导（四维关注 cell 的求和）

| 步 | 操作 | (*,*,*) | (a,*,*) | (*,b,c) | (a,b,c) |
|---|---|---|---|---|---|
| 1 | Add(a,b,c,+2) | 2 | 2 | 2 | 2 |
| 2 | Add(a,b,c,+3) | 5 | 5 | 5 | 5 |
| 3 | Add(a,d,c,+5) | 10 | 10 | 5 | 5 |
| 4 | Add(e,b,c,+7) | 17 | 10 | 12 | 5 |
| 5 | Remove(a,b,c,+2) | 15 | 8 | 10 | 3 |

- (甲) 第 5 步后 `(*,*,c)`=15（全部事实 C 都是 c）、`(a,*,c)`=8（A=a 的 +2+3+5−2）。纯 ROLLUP(A,B,C) 只有 (*,*,*)/(a,*,*)/(a,b,*)/(a,b,c) 四个层级，这两个 cell **不存在**，查询只能报「无此分组/0」，15 和 8 都拿不到。
- (乙) 再 Add("",b,c,+4)：正确实现 `(*,*,*)`=19、`(*,b,c)`=14，并新增 level3 cell `("",b,c)`=4。若用 `""` 当 ALL 哨兵：A 维「具体空串」与 ALL 碰撞，8 个掩码塌缩成 4 个不同键、每个被加两次——`(*,*,*)` 被算成 15+8=**23**、`(*,b,c)` 被算成 10+8=**18**，且 `("",b,c)` 根本建不出来。
- (丙) 第 5 步后非空 cell 共 **16** 个（level0×1、level1×5、level2×7、level3×3）。笛卡尔积法物化 (2+1)×(2+1)×(1+1)=**18** 个候选，其中 2 个空 cell（(e,d,c)、(e,d,*)）。取值数为 nA,nB,nC 时候选数 =(nA+1)(nB+1)(nC+1)=O(nA·nB·nC)，随取值数立方膨胀且含大量空 cell；逐事实 8-掩码法每事实恒触 8 键、只建非空 cell，与取值基数无关。

## 四条不变量的保证位置与钉住测试

1. 批量一致：`cube/cube.go` 的 `apply` 对 8 掩码逐键 ±V；测试 `TestBatchEquivalence`（api/api_test.go，随机序列对拍批量重算）。
2. 增量自洽：`apply` 的 Remove 走同一 8 掩码 −V，求和为 0 即删键；测试 `TestAddRemoveRoundTrip`。
3. 层级守恒：`dim/dim.go` 的 `Cells` 按位枚举 8 键、`Level` 数非 ALL 维；测试 `TestLevelInvariant`（分布恒 1/3/3/1）。
4. 失败不留痕：`cube.apply` 先全量模拟、超限即整体拒绝不落盘，`api.New` 拒非正 maxCells、Remove 前查 facts 计数；测试 `TestFailureAtomicity`。

另：触碰计数器为 cube 非导出字段，由白盒测试 `cube.TestTouchedIsConstant8` 钉住（m=100..10000 恒为 8）；并发只读一致性由 `TestConcurrentViews` 钉住。
