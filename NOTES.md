# ontology-342 推导与不变量（window=4，保留窗口 [max-3,max]）

十步表（事件 / 判定 / max / 步后保留表 / 改状态）：

1. (1,a) ② max=1 {1:a} 改
2. (2,b) ② max=2 {1:a,2:b} 改
3. (3,c) ② max=3 {1:a,2:b,3:c} 改
4. (2,b) ③ max=3 同表 不改
5. (3,x) ④拒 max=3 同表 不改
6. (4,d) ② max=4 {1:a,2:b,3:c,4:d} 改
7. (1,z) ④拒 max=4 同表 不改
8. (5,e) ②逐出1 max=5 {2:b,3:c,4:d,5:e} 改
9. (2,b) ③ max=5 同表 不改
10. (1,a) ⑤拒 max=5 同表 不改

甲：区分靠“值相等”而非“位点存在”：4 的值 b 与留存一致→③幂等；5 的 x≠留存 c→④冲突。若“同位点即冲突”，第 4 步错报成冲突，4/5 皆冲突不可区分，合法重投被拒；若“只按 Value 判重不看位点”，x 是未见过的新值→第 5 步误判生效，第 8 步后视图位点 3 错成 x（应为 c）。

乙：第 10 步正确判定是⑤回卷拒绝（1<窗口下沿 2，值已逐出、无法核验）。窗口误写成 [max-4,max]（宽 5）：第 8 步后位点 1 仍在表，(1,a) 值匹配→第 10 步误判③幂等成功；窗口误写成 [max-2,max]（宽 3）：第 9 步时窗口为 [3,5]，(2,b) 已不在表→误判⑤回卷拒绝。

丙：若改成 upsert，第 5 步把 3 覆盖成 x、第 7 步把 1 覆盖成 z；第 8 步逐出 1 后视图为 {2:b,3:x,4:d,5:e}，位点 3 错成 x——异载荷重投静默改写已生效状态，最终状态取决于重投次数，exactly-once 破产。第 10 步 (1,a) 虽与历史值完全相同仍须拒绝：位点 1 已逐出，系统无法核验它是原 (1,a) 的重投、旧数据重放、还是恰合同值的另一条消息，没有留存值可比对，唯一安全判定是⑤拒绝并交上游处理。

不变量（代码保证位置 / 钉住的测试函数）：

1. 朴素参照一致：dedup.SelfCheck→naiveRebuild 只取生效日志、按 (分区,位点) 建表并重放逐出，与在线 View 逐对比较；TestInvariantNaiveReference（随机序列循环）。
2. 幂等：rec.Apply 的③分支只做一次只读比对、不写任何字段；TestInvariantIdempotent。
3. 位点单调：rec.Apply 中 max 只在②分支被赋新值，其余分支零写入；TestInvariantMonotonicMax。
4. 失败不留痕/整批原子：rec 的①④⑤分支不写状态，dedup.ApplyBatch 先在克隆上暂存、任一被拒即丢弃克隆，提交阶段才换入；TestInvariantRejectLeavesNoTrace、TestBatchAtomic。

并发：dedup 用 sync.RWMutex 保护，View 持读锁、SelfCheck 在独立实例上核验；TestConcurrentView（go test -race）。
