# NOTES
八步表（pageSize=4；格式 页(引用计数,内容)）：
0. 初始：A(1,[0000])，V0→A
1. Snapshot(V0)→V1：A(2,[0000])
2. Snapshot(V0)→V2：A(3,[0000])
3. Write(V1,0,9)：拷贝 A→B 再写；A(2,[0000])，B(1,[9000])，V1→B
4. Write(V0,0,5)：拷贝 A→C；A(1,[0000])，B(1,[9000])，C(1,[5000])，V0→C
5. Release(V2)：A 1→0 释放；存活 B(1,[9000])，C(1,[5000])
6. Write(V0,1,7)：C 独占，原地写 C(1,[5700])
7. Snapshot(V0)→V3：C(2,[5700])，B(1,[9000])
8. Read(V1,0)=9，Read(V0,0)=5；B(1,[9000])，C(2,[5700])
(甲) 第3步若原地写：A=[9000]，Read(V0,0)、Read(V2,0) 都错返 9（正确应为 0），V0/V2 被污染。
(乙) 拷贝后忘记减旧页：第3步后 A=3（正确 2）；第4步后 A=2（正确 1），Release(V2) 后 A=1 永不归零，A 泄漏（[0000] 占用页槽）。
(丙) 首次 Release(V1)：B 1→0 释放、V1 失效；第二次无重复检测则按旧页号再 free——槽位若已被新页复用，会误 free 掉该存活页（可能正被 V0 使用），其他 view 悬垂、读到已释放内存、RefCount 错乱甚至 panic；未复用则计数被打成 −1，状态损坏。
不变量 → 代码位置 → 钉住它的测试：
1 引用计数守恒：mgmt/manager.go 的 alloc/snapshot/release 与 decr 归零释放；TestRefCountConservation、TestConcurrentSnapshotsWrites
2 与朴素参照一致：cow/page.go 的 Clone 与 mgmt Write 的 refs>1 先拷分支；TestNaiveModelEquivalence
3 写隔离：cow Clone 整页拷贝 + mgmt Write 改指新页；TestWriteIsolation
4 失败不留痕：mgmt 各操作先全量校验、超限在拷贝前判定，最后才改态；TestFailureAtomicity
复杂度 O(1)：mgmt 非导出字段 lastWriteChecks（每页一个计数，只查 1 条），不出现在公开接口；TestWriteCheckCountConstant
四类哨兵错误：mgmt ErrInvalidView/ErrOffsetOutOfRange/ErrBadDataLen/ErrPageLimit；TestErrorsTableDriven
自检：api/manager.go SelfCheck 跑内置八步+四类拒绝，深层守恒调 mgmt.Validate；TestAPISelfCheck
