# 分区亲和路由与迁移 NOTES

## 一、八步推导（P0/P1/P2 初始 Active、空；每行：各分区集合；Count(P0,P1,P2)；结果）

1. Assign(a,P0)：{a}/{}/{}；1,0,0；成功
2. Assign(b,P0)：{a,b}/{}/{}；2,0,0；成功
3. Assign(c,P1)：{a,b}/{c}/{}；2,1,0；成功
4. Assign(d,P2)：{a,b}/{c}/{d}；2,1,1；成功
5. BeginDrain(P2)：{a,b}/{c}/{d}；2,1,1；成功（P2→Draining）
6. Assign(e,P2)：{a,b}/{c}/{d}；2,1,1；被拒：分区 Draining，新亲和被拒
7. Put(d,"x")：{a,b}/{c}/{d}；2,1,1；被拒：d 的亲和分区 Draining，Put 要求 Active
8. Migrate(P2→P0)：{a,b,d}/{c}/{}；3,1,0；成功（P2→Removed，d 整体改亲和到 P0）

（甲）只累加 to、不清空 from：Count(P2) 错成 1，全表和错成 5（d 被双计）。注：题面所给“正确总数=3”与其不变量 1 冲突——被接受的 Assign 为 a,b,c,d 共 4，正确全表和应为 4（3 是 P0 单表计数）；代码/测试/自检按不变量 1 取 4。
（乙）第 7 步静默放行：最终错成 Get(d)="x",true。会被第 8 步带走：d 本就是 P2 的键，迁移把 d 的亲和整体改到 P0，而值按 key 全局存放、迁移不清值，故迁移后从 Active 的 P0 读到 x（正确为空串+不存在）。
（丙）第 6 步放行：e 混入排空批，第 8 步一并迁到 P0，最终 e 属 P0，Count(P0) 错成 {a,b,d,e}=4（正确 3）。故 Draining 必须冻结键集合，迁移才是“闭集上的原子整体”；不变量 1 必须写“只统计被接受的 Assign”，正因 e 这类被拒声明不得产生归属、也不得进入批量重算。

## 二、四条不变量：代码保证位置 / 钉住它的测试函数

1. 与批量重算一致：router.go 的 Assign 增 owner 与 owner→keys 索引、Migrate 在同一把写锁下整体改键，Count 直接读索引计数——TestCountMatchesBatchRecompute
2. 迁移一致性：router.go 的 Migrate 先取 from.Keys() 快照，再逐个改 owner、搬集合、MarkRemoved；from 必空、to 恰增这批、owner 永不指向 Removed——TestMigrateConsistency、TestEightStepDerivation
3. 排空与冲突：router.go 的 Assign/Put 在落盘前判分区状态（非 Active 即 ErrAffinityConflict），Get 只查亲和存在性，Draining 仍可读——TestEightStepDerivation、TestRejectedOpsLeaveNoTrace
4. 失败不留痕：router.go 所有写操作持写锁后先完成全部校验、再一次性改状态，校验失败零修改——TestRejectedOpsLeaveNoTrace

并发原子：router 用 sync.RWMutex，Get/Count/SelfCheck 走 RLock 或自有实例，Migrate 全程 Lock——TestConcurrentReadersAndMigrate（api_test.go）。
扫描计数：owner→keys 索引即 part.Partition 自带键集合，Migrate 只检查 from 一个分区，scanCount 恒为 1——TestScanCountBound（白盒直读非导出字段，不经导出方法）。
