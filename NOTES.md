# NOTES — 半朴素传递闭包推导

样例边：ab, bc, cd, bd, de, eb（b→c→d→e→b 成环）。
| 轮 | Delta | 候选数 | 新增 | 去重丢弃 | path |
|--|--|--|--|--|--|
| 0 | {ab,bc,cd,bd,de,eb} | 0 | 6 | - | 6 |
| 1 | {ac,ad,ce,be,db,ec,ed} | 8 | 7 | {bd} | 13 |
| 2 | {ae,cb,bb,dc,dd,ee} | 8 | 6 | {ad,ed} | 19 |
| 3 | {cc} | 8 | 1 | {ab,bc,bd,cd,dd,de,eb} | 20 |
| 4 | ∅ | 1 | 0 | {cd} | 20（停） |

(甲) R2 半朴素用 D1(7) join 出 **8** 候选；若用全量 path1(13) 则 **16** 候选，多出的 8 个是
重推导，如 ad（ab 配 bd）、ed（eb 配 bd），另有 bd/ce/db/ec 等。
(乙) 闭包共 **20** 元组；自环为 {bb,cc,dd,ee}（无 aa：无结点可达 a）。固定只跑 2 轮停 →
**19** 个，唯一漏掉 **cc**（cc 在 R3 经 cb+bc 首次导出，R4 才证不动点）。
(丙) a→d 两条导出：① R1 经 z=b：(a,b)∈D0 配边 bd；② R2 经 z=c：(a,c)∈D1 配边 cd，
候选 ad 已在 path → 去重丢弃。不去重（多重集）时环上每轮生成长度 4,8,12,… 的回路，
b→b 等无限追加，永无不动点、不终止。

不变量与保证位置 / 钉住测试：
1. 与朴素闭包一致：semi.Eval 集合语义 + api.referenceClosure(BFS) 比对；TestClosureMatchesBFS
2. 终止且末轮 Delta 空：semi/semi.go Eval 的 delta 空即停；TestFixpointLastDeltaEmpty
3. 每元组恰入 Delta 一次：semi.go join 后减 path 且并入；TestDeltaEnteredOnce
4. 失败不留痕：api.go New 先整体校验再建状态；TestRejectedLeavesNoTrace
候选计数（非导出）：semi.engine.candTotal，TestChainCandidateCount；并发：TestConcurrentEval
