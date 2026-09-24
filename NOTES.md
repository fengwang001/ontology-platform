# NOTES：三表增量连接推导

八步状态（×n 为多重度；末列为结果条数）：
1. Ins S(1,10)  : R{} S{(1,10)×1} T{} → J{} → 0
2. Ins T(10,100): R{} S{(1,10)×1} T{(10,100)×1} → J{} → 0
3. Ins R(5,1)   : R{(5,1)×1} → J{(5,1,10,100)×1} → 1
4. Ins S(1,10)  : S{(1,10)×2} → J{(5,1,10,100)×2} → 2
5. Ins R(6,1)   : R{(5,1)×1,(6,1)×1} → J{(5,1,10,100)×2,(6,1,10,100)×2} → 4
6. Del R(5,1)   : R{(6,1)×1} → J{(6,1,10,100)×2} → 2
7. Del S(1,10)  : S{(1,10)×1} → J{(6,1,10,100)×1} → 1
8. Del S(1,10)  : S{} → J{} → 0

(甲) 第 4 步真值=2；若错当集合（第二次 Insert 幂等）：第 4 步错成 1，第 5 步错成 2（真值分别为 2、4）。
(乙) 第 6 步应移除 2 条；错成“只删 1 条不级联”：条数错成 3，僵尸 (5,1,10,100)×1 残留。
(丙) 第 7 步应移除 1 条、S 剩 1 份 (1,10)；错成“删光全部副本”：S 清零、移除 2 条，条数错成 0（真值 1）。

不变量（代码保证位置 / 钉住的测试函数）：
1. 与批量重算一致：join.go 的 delta 中 R/S/T 三个 case 只对受影响四元组增量加减 — TestEquivalentToBatchRecompute
2. 多重集语义：rel/relation.go 逐副本计数；delta 权重 cs*ct、cr*ct、cr*cs 相乘放大 — TestEightSteps、TestMultisetProduct
3. 级联一致：join.go Delete 复用同一 delta 枚举“全部”匹配组合并整量扣减 — TestEightSteps（第6/7步断言）
4. 失败不留痕：Insert 先算 delta、超限先返回；Delete 先判 Count==0；非法表名 delta 即返回 ErrBadTable，均早于任何状态修改 — TestRejectedOpsAtomic
另：候选对计数 TestCandidatesNoScan / TestCandidatesBounded；并发 TestConcurrentMatchesSerial；自检 TestSelfCheck。
