# NOTES

S=2,T=3；事件序：(a,0)(a,0)(a,0)(b,1)(a,0)(a,0)(c,1)(a,0)。cnt 按分片号升序；分片2=分给 a 的专属分片。

| # | 事件 | 落分片 | 触发迁移 | cnt |
|---|---|---|---|---|
| 1 | (a,0) | 0 | false | [1,0] |
| 2 | (a,0) | 0 | false | [2,0] |
| 3 | (a,0) | 0 | true | [3,0,0]（触发事件落基础分片，随后才分配分片2） |
| 4 | (b,1) | 1 | false | [3,1,0] |
| 5 | (a,0) | 2 | false | [3,1,1] |
| 6 | (a,0) | 2 | false | [3,1,2] |
| 7 | (c,1) | 1 | false | [3,2,2] |
| 8 | (a,0) | 2 | false | [3,2,3] |

(甲) 正确结果 [3,2,3]；若触发事件直接落专属分片 => [2,2,4]。
(乙) 触发事件在基础/专属双写 => [3,2,4]，总和 9 比应有的 8 多 1。
(丙) 阈值误写成 >T：a 在其第 4 个事件（总序第 5 步）才迁移 => [4,2,2]。

## 不变量：保证位置 / 钉住的测试

1. 总量守恒：route/route.go 的 Apply 对每个接受事件恰给一个分片 +1；api/api.go 的 Feed 先整批校验再落状态。测试 TestConservation。
2. 与朴素参照一致：route.go 的路由（hot→dedicated，否则 base）与 api.go Counts 快照；逐事件暴力模拟比对。测试 TestCountsMatchesNaiveReference。
3. 迁移原子性：route.go Apply 中触发事件先计入 base、再 MarkHot+Alloc，之后事件才落专属。测试 TestCanonicalEightSteps、TestMigrationAtomicity。
4. 失败不留痕：api.go Feed 先校验全批，任一非法则不触碰任何状态。测试 TestRejectedBatchLeavesNoTrace。
