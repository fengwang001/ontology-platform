# NOTES — 列式物化视图：八步推导与不变量

## 八步分步表（列序 A B C；T=活 F=死；列内容按槽列出）

| # | 操作 | a | b | c | alive | slotOf | 结果 |
|---|------|---|---|---|-------|--------|------|
| 1 | Insert(10,20,30) | [10] | [20] | [30] | [T] | [0] | rowID=0 |
| 2 | Insert(1,2,3) | [10,1] | [20,2] | [30,3] | [T,T] | [0,1] | rowID=1 |
| 3 | Insert(100,200,300) | [10,1,100] | [20,2,200] | [30,3,300] | [T,T,T] | [0,1,2] | rowID=2 |
| 4 | Delete(1) | 同前 | 同前 | 同前 | [T,F,T] | [0,-1,2] | ok（列里旧值保留） |
| 5 | Insert(7,8,9) | [10,1,100,7] | [20,2,200,8] | [30,3,300,9] | [T,F,T,T] | [0,-1,2,3] | rowID=3（追加末尾，不复用墓碑） |
| 6 | Compact() | [10,100,7] | [20,200,8] | [30,300,9] | [T,T,T] | [0,-1,1,2] | ok（存活绑定不变） |
| 7 | Update(2,C,999) | 同前 | 同前 | [30,999,9] | [T,T,T] | [0,-1,1,2] | ok（slotOf[2]=1，改槽1） |
| 8 | Get(2) | — | — | — | — | — | (100,200,999) |

- (甲) 正确：slotOf[2]=1，改槽1 的行 (100,200,300)→C=999，Get(2)=(100,200,999)。若 Compact 不重建 slotOf（沿用 [0,1,2,3]）：Update 改到槽2（实为 rowID3 的行），c 错成 [30,300,999]，Get(2) 错返回 (7,8,999)。
- (乙) 第4步后正确 Project([B])=[20,200]；若忘跳墓碑直接返回整列则 [20,2,200]，多出墓碑值 2。
- (丙) 正确报 ErrDeleted（已删）；若 slotOf[1] 未置 -1 且 Get 不查 alive，错返回陈旧值 (1,2,3)。

## 四条不变量：保证位置与钉住测试

1. 行式参照一致：col 按 slotOf 直取、view.Project 从同一存活快照切列；测试 api_test.TestReferenceModel（多档随机操作序列对拍 map 参照）。
2. 稳定 rowID：col.Delete 只置墓碑不改列值，col.Compact 重建 slotOf 且死 id 仍为 -1；测试 api_test.TestStableRowIDAcrossCompact。
3. 投影对齐：view.Project 各列切自同一次 col.Snapshot；测试 api_test.TestProjectAlignment（7 个非空列子集）。
4. 失败不留痕：col.Update/Delete/Get 与 view.Project 全部先校验后读写；测试 api_test.TestFailureNoTrace（含三类哨兵错误互不相同）。

另：Get 检查槽数上界由 col 非导出字段 lastGetChecks 记录，col_test.TestGetChecksIndependentOfM 钉住 O(1)；并发只读由 api_test.TestConcurrentReadOnly 钉住；api.SelfCheck 在内置序列上复核上述四条。
