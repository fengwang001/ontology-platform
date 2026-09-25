# NOTES — ontology-482

八步推演（key 均为 "k"）：

| 步 | 操作 | 引用计数变化 | Distinct("k") |
|---|---|---|---|
| 1 | Upsert(1,"k","a",10) | (a,10) 0→1 | 1 |
| 2 | Upsert(2,"k","a",20) | (a,20) 0→1 | 2 |
| 3 | Upsert(3,"k","b",10) | (b,10) 0→1 | 3 |
| 4 | Upsert(4,"k","a",10) | (a,10) 1→2（不翻转） | 3 |
| 5 | Upsert(1,"k","a",30) | (a,10) 2→1；(a,30) 0→1 | 4 |
| 6 | Delete(4) | (a,10) 1→0，元组删除 | 3 |
| 7 | Upsert(2,"k","b",10) | (a,20) 1→0；(b,10) 1→2 | 2 |
| 8 | Delete(3) | (b,10) 2→1（不翻转） | 2 |

- 甲：第 2 步正确 = 2；若按单列 Col1 去重，两行 Col1 都是 "a"，错成 **1**。
- 乙：第 6 步正确 = 3；Delete 不撤回则 (a,10) 引用计数虚高为 1、被错误保留，错成 **4**。
- 丙：第 4 步正确 = 3（两行共享 (a,10) 只计一次）；每行当独立元组不去重则错成 **4**。

不变量保证位置 / 钉住测试：

- I1 与批量重算一致：`dd/dd.go` 的 add/retract 只在引用计数 0↔1 翻转时改 distinct，Total 求和分组；TestRandomOpsMatchBatch 用暴力重算逐操作比对 Distinct 与 Total。
- I2 计数精确：`tup/tup.go` Group.Add/Remove 维护 refs 与 distinct；TestTupleRefCount 与 TestRandomOpsMatchBatch 钉住。
- I3 撤回对称：`dd/dd.go` Upsert 旧元组恰好 −1、新元组恰好 +1，Delete 只 −1；TestRandomOpsMatchBatch（含同 key/跨 key 移动与删除，逐操作对拍批量重算）钉住。
- I4 失败不留痕：`dd/dd.go` Upsert/Delete 在加锁改状态前先校验 rowID、key、行存在性；TestRejectedOpsLeaveState（三个哨兵错误互不相同）。
- 复杂度：dd 非导出字段 checks 每触及一个元组加 1；TestCheckCountBounded 断言 m=100…10000 时单次操作增量 ≤ 2，不随 m 增长。
- 并发：`dd/dd.go` 单一 mutex 保护；TestConcurrentUpsert 断言 N 路 Upsert 后 Distinct("k")==N 且并发读到的值单调不减。
