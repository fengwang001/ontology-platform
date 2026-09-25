# NOTES

## 八步推导（全部行 key="k"；只列引用计数发生变化的元组）
| 步 | 操作 | 引用计数变化 | 步后 Distinct |
|---|---|---|---|
| 1 | Upsert(1,k,a,10) | (a,10) 0→1 | 1 |
| 2 | Upsert(2,k,a,20) | (a,20) 0→1 | 2 |
| 3 | Upsert(3,k,b,10) | (b,10) 0→1 | 3 |
| 4 | Upsert(4,k,a,10) | (a,10) 1→2（无翻转） | 3 |
| 5 | Upsert(1,k,a,30) | (a,10) 2→1；(a,30) 0→1 | 4 |
| 6 | Delete(4) | (a,10) 1→0（翻转） | 3 |
| 7 | Upsert(2,k,b,10) | (a,20) 1→0；(b,10) 1→2 | 2 |
| 8 | Delete(3) | (b,10) 2→1（无翻转） | 2 |

终态活跃行：1=(a,30)、2=(b,10)，Distinct=2。
- (甲) 第 2 步后正确=2；若按单列 Col1 去重，两行 Col1 都是 "a"，错成 1。
- (乙) 第 6 步后正确=3；Delete 不撤回则 (a,10) 被错误保留（计数停在 1），错成 4。
- (丙) 第 4 步后正确=3；每行当独立元组不去重则错成 4。

## 不变量保证位置与钉住测试
1. 与批量重算一致：distinct/total 的唯一改写点是 tup/tup.go 的 Catalog.Adjust 里 0↔1 翻转分支，dd Upsert/Delete 严格按规则 ±1；TestUpsertRetractMatchesBatch 每步与批量重算比对。
2. 计数精确：tup/tup.go 的 Catalog.Adjust 每次恰改 ±1（返回 before/after），降到 0 即删键；TestUpsertRetractMatchesBatch 逐元组比对计数。
3. 撤回对称：dd/dd.go Upsert 先对旧 (key,元组) adjust -1 再对新元组 +1，Delete 恰撤回一次；TestRetractSymmetry。
4. 失败不留痕：dd/dd.go Upsert/Delete 的 rowID/Key/存在性校验全部在任何引用计数改写之前完成；TestRejectedOperationsLeaveNoTrace。
