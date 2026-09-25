# ontology-633 推导与不变量索引

## 八步表（空回收器；A、B 为前两步 Root 的返回值）

| # | 操作 | rc[A] | rc[B] | 本步释放 |
|---|---|---|---|---|
| 1 | Root()→A | 1 | — | 无 |
| 2 | Root()→B | 1 | 1 | 无 |
| 3 | Point(A,B) | 1 | 2 | 无 |
| 4 | Point(B,A) | 2 | 2 | 无 |
| 5 | Unroot(A) | 1 | 2 | 无 |
| 6 | Unroot(B) | 1 | 1 | 无 |
| 7 | Collect() | 已释放 | 已释放 | A、B（2 个） |
| 8 | 观察 | — | — | 无（均已回收） |

(甲) 纯引用计数不扫边，两步 Unroot 后 rc 恒为 1，谁都不释放；A↔B 不可达环永久泄漏。
(乙) rc[X]=2（根+Y→X），rc[Y]=1（X→Y）；tmp[X]=2-1=1，tmp[Y]=1-1=0；不救援会误 free Y，X.child 成为悬垂指针，根可达遍历踩到已释放对象。
(丙) 应收 A：tmp[A]=1，自引边 A→A 也减一得 0，判不可达后 free；不减自引则 tmp[A]=1 误判有根被标活，自引垃圾永久泄漏。

## 不变量：代码保证位置 / 钉住测试

1. rc 守恒：refc/refc.go 的 Root/Unroot/releaseFromField/Point（先加新边后删旧边）与 Sweep 对跨边界存活子节点补减 fields；TestRefCountConservation。
2. 与朴素可达一致：trial/trial.go Collect 的 tmp=rc 起手→减全部字段边→roots 残余>0 者沿 child 救援→未标记者清扫；TestReachabilityVsNaive、TestEightStepTable。
3. 无悬垂：refc.go 级联沿 child 先取边后删记录、Sweep 先处理跨边界边再删除；trial.go 救援只走 child map；TestNoDangling、TestReachabilityVsNaive。
4. 失败不留痕：trial.go Unroot/Point 先查存活与 roots>0、Root 先查上限，全部检查在任何写操作之前；TestRejectedOpsLeaveState。
