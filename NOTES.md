# NOTES — ontology-434

M=3,maxFanIn=2。R run0=[1,3,5] run1=[2,3,7] → 归并 R=[1,2,3,3,5,7]；S run0=[3,3,6] run1=[2] → 归并 S=[2,3,3,6]。

步 | R键 S键 | 比较 | 动作 | 本步输出
1 | 1 2 | < | 推进R | 无
2 | 2 2 | = | 整段笛卡尔(1×1) | (2,2)×1
3 | 3 3 | = | 整段笛卡尔(2×2) | (3,3)×4
4 | 5 6 | < | 推进R | 无
5 | 7 6 | > | 推进S | 无
6 | 7 越界 | — | S耗尽,结束 | 无

正确：step2=1、step3=4，总5条。
甲：第3步只出1条；该写法下一轮仍为(3,3)又出1条，代码实跑总输出3条（漏1条；若相等段只相遇一次则为2条）。
乙：相等也推进R，全程无配对，总输出0条。
丙：R只剩run0=[1,3,5]，缺Key 2、7及重复3中的一条；与S=[2,3,3,6]连接只剩 R3×S3×3=2条，由正确5条错成2条。

## 不变量（代码位置 / 钉住它的测试）

1. 输出多重集=朴素嵌套循环：api/api.go 的 Join()（经 mrg.Joiner.Join 整段笛卡尔）/ TestInvariant_NestedLoopEquivalence
2. 归并序列是排序且多重集不变：mrg/mrg.go 的 Sorted()（merger 堆选最小、并列取 run 编号小者）/ TestInvariant_MergedSorted
3. run 除末尾外恰 M 条、末尾 ≤M、块内升序：run/run.go 的 Build() / TestInvariant_RunStructure
4. 非法 M/fanIn/负键整体失败不留痕：run.Build 全量预校验后才生成，api.BuildR/BuildS 失败不赋值 / TestInvariant_RejectionAtomic

复杂度非导出计数器 merger.probes 在 mrg/mrg.go，钉于 TestHeapProbeBound（每次取最小检查数 ≤ 2⌈log₂m⌉+2）；并发只读钉于 TestConcurrentJoin；内置输入自检钉于 TestSelfCheck。
