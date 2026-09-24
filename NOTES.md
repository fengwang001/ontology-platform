# 位点↔时间戳索引：推导与不变量

八步序列 ts = 2,1,8,3,4,9,5,7（maxRecords=8）：

| 步 | off | ts | pm=max(ts[0..off]) |
|---|---|---|---|
| 1 | 0 | 2 | 2 |
| 2 | 1 | 1 | 2 |
| 3 | 2 | 8 | 8 |
| 4 | 3 | 3 | 8 |
| 5 | 4 | 4 | 8 |
| 6 | 5 | 9 | 9 |
| 7 | 6 | 5 | 9 |
| 8 | 7 | 7 | 9 |

pm=[2,2,8,8,8,9,9,9]。
(甲) SafeOff(7)=1（off≥2 时 pm=8>7）；错解「最后一条 ts≤7」线性扫得 7（off=2 的 8 被无视）。
(乙) SafeOff(8)=4（off≥5 时 pm=9）；若前缀聚合为最小值 [2,1,1,1,1,1,1,1]，二分「min≤8」全满足，错成 7。
(丙) SafeOff(2)=1：pm[0]=pm[1]=2 都 <=2（pm[0] 恰等值，用 <= 包含边界，且第 2 条 ts=1 未抬高 pm，故最长前缀到 off 1）；写成严格 < 连 off 0 都不满足，得 -1/found=false。空日志 N==0 返回 ErrEmptyLog。

不变量（代码位置 / 钉住的测试）：
1 前向精确：bidx.Append 顺序存 ts、TSAt 直取 x.ts[off] / TestForwardTSAt。
2 与朴素参照一致：bidx.SafeOff 对非递减 pm 二分 / TestSafeOffNaive（随机多档）。
3 pm 非递减：tsl.Next 增量 max(prev.PM,ts) / TestPrefixMaxMonotonic；TestSelfCheck 复核。
4 失败不留痕：api 三个入口先判定后改状态 / TestRejectedOpsNoTrace；TestSentinelErrorsDistinct 钉哨兵。
并发只读一致：RWMutex 读锁 / TestConcurrentReaders（go test -race）。
