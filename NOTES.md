# ontology-389 推导（R=3，key="k"；100 表示 (1,0,0)，F=存活 T=墓碑）
步 | clk[0] clk[1] clk[2] | Stable | store["k"] | GC移除
1 Write0 a (100) | 100 000 000 | 000 | (100,"a",F) | 无
2 Sync1 (100) | 100 100 000 | 000 | (100,"a",F) | 无
3 Del1 (110) | 100 110 000 | 000 | (110,"",T) | 无
4 Sync0 (110) | 110 110 000 | 000 | (110,"",T) | 无
5 GC | 110 110 000 | 000 | (110,"",T) | 无（110 不逐分量<=000）
6 Write2 "stale"(100) 旧 | 110 110 100 | 100 | (110,"",T) | 无（写被忽略，clk[2]仍取max）
7 Sync2 (110) | 110 110 110 | 110 | (110,"",T) | 无
8 GC | 110 110 110 | 110 | 不存在（记入 gone=110） | k
甲：正确实现第5步移除「无」、第8步移除 [k]。错判据只看删除者 clk[1]=110，则第5步即提前回收 k；第6步 (100) 的旧写因墓碑已消失被当成首写接受，k 被复活成 "stale"（丢删除）。
乙：判据改严格小于：第8步 110==Stable 不满足 <，GC 返回空、k 仍以墓碑残留；边界相等的墓碑永远等不到回收，墓碑泄漏堆积（正确实现返回 ["k"]）。
丙：「严格更大才覆盖、相等忽略」与正确判据 v<=赢家即忽略 完全等价；(110) 严格大于 (100)，第3步照常覆盖删除，最终 View 不含 k，无错值——本例不暴露差异；真正危险的反向错误是「相等也覆盖」。
## 四条不变量（代码位置 / 钉住的测试，测试均为 gc 包内部测试 package gc）
1. 与批量重算一致：gc/gc.go 的 merge 字典序赢家判定 + gone 抑制旧向量复活 + GC 弹出；gc.TestViewOracle（随机序列对照独立 oracle refModel）。
2. 稳定向量单调：gc/gc.go 统一入口 Apply 中 clk[rep] 逐分量 MergeMax（Write/Delete/Sync 都经 Apply），Stable 逐分量取 min；gc.TestStableMonotonic。
3. GC 安全不丢更新：gc.GC 只从最小堆头部弹出 vec<=Stable 者并转入 gone，旧向量事件被 merge 的 gone 判定挡回；gc.TestGCSafety。
4. 失败不留痕：gc.Apply/check 先整批校验（哨兵 ErrInvalidR/ErrReplica/ErrVector/ErrKey）通过后才碰状态；gc.TestRejectedLeavesNoTrace。
复杂度：gc.Store.probeN 为非导出字段，gc.TestGCProbeCount 断言不随 m 线性增长；并发只读一致性由 gc.TestConcurrentViews 钉住（-race）；八步推导由 gc.TestEightSteps、对外自检由 api.Engine.SelfCheck 钉住。
