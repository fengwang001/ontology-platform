# 推导
# 1. Add[1,3) => [1,3)
# 2. Add[3,5) => [1,5)：端点 3 相接，右开不含 3 与左区间含 3 正好拼成连续点集。
# 3. Add[7,9) => [1,5),[7,9)：5 与 7 之间缺整点 5、6。
# 4. Add[5,5) => [1,5),[7,9)：空区间没有任何整点，是恒等操作。
# 5. Add[5,7) => [1,9)：同时贴住左区间右端 5 和右区间左端 7，三者合一。
# 6. Remove[2,8) => [1,2),[8,9)：删掉内部后只剩左右两个非空残段。
# 合并式选定 a.hi >= b.lo（且按 lo 排序）：第 1、2 步中 3>=3，必须合并；若写成 a.hi > b.lo，[1,3) 与 [3,5) 会被错留为相邻两段。第 3 步 5>=7 为假，两种写法都不合并；因此只有 >= 同时兼容相接与相离。
# [5,5) 会在左端点处贴住 [1,5)，但它不含任何点，不能作为普通区间参与重叠/相邻关系；否则中间形态会放入零宽区间，违反“不存在空区间”，且最大端点处还会诱导端点加一。故 Add/Remove 在进入合并前静默忽略 lo==hi（不加不减，必然成功）。

# 不变量保证
# 1. 规范形唯一：iv.Valid/Empty 先拦截非法与空区间；set.canonical 排序、去重并以 hi>=lo 合并。由 TestCanonicalOperations 钉住。
# 2. 逐点一致：set.Contains 只比较 lo<=x 与 x<hi；TestRandomOperationsMatchNaive 用随机序列和 map 逐点钉住。
# 3. 移除可分裂：set.Add 的删除分支保留 [old.lo,op.lo) 与 [op.hi,old.hi)，再丢弃空段。由 TestRemoveSplits 钉住。
# 4. 失败不留痕：set.Add 先在副本上 canonical，超限再返回 ErrTooManySpans；api.Apply 整批复制后提交。由 TestRejectedOperationsAtomic 钉住。
# 复杂度：set.probeCount 由 sort.Search 定位，只统计落点后的合并扫描；TestBinarySearchComparisonCount 钉住多档 m。
# 并发：set.mu 保护写复制，Contains/Spans 在 RLock 下使用快照；TestConcurrentReadOnly 钉住 race 与逐元素一致。
