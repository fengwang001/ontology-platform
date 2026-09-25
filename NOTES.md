# NOTES — 等宽直方图增量维护

## 推导：width=10, anchor=0，依次 Insert 5, 12, 20, 10, -3, -15

| 步 | v | k=floor(v/10) | 桶区间 | 本步后各桶计数（minBucket..maxBucket） |
|---|---|---|---|---|
| 1 | 5 | 0 | [0,10) | 0:1 |
| 2 | 12 | 1 | [10,20) | 0:1, 1:1 |
| 3 | 20 | 2 | [20,30) | 0:1, 1:1, 2:1 |
| 4 | 10 | 1 | [10,20) | 0:1, 1:2, 2:1 |
| 5 | -3 | -1 | [-10,0) | -1:1, 0:1, 1:2, 2:1 |
| 6 | -15 | -2 | [-20,-10) | -2:1, -1:1, 0:1, 1:2, 2:1 |

- (甲) -3 正确入桶 -1。Go 整数除法向零截断：-3/10=0，错入桶 0。此时 BucketCount(-1) 错成 0（应为 1），BucketCount(0) 错成 2（应为 1）。
- (乙) 20 是桶 1 的右边界，半开区间应入桶 2。若误用闭区间，20 错入桶 1。此时 BucketCount(1) 错成 3（应为 2），BucketCount(2) 错成 0（应为 1）。
- (丙) 固定数组只覆盖 [0,3) 时，-3 与 -15 被静默丢弃，Total() 错成 4（应为 6），丢的是 -3 和 -15。

## 四条不变量：保证位置与钉住它的测试

1. 与批量重算一致：Insert 只经 bucket.Index 算 k 后 count[k]++（hist/hist.go Insert），与批量重算同一条公式 → 测试 TestInsertMatchesBatch。
2. 计数守恒：hist.Insert 中 total 与 count[k] 同步 ++，无丢弃分支 → 测试 TestTotalConservation。
3. 范围完整：越界时 Insert 只扩展 min/max 不删计数；范围外桶从未写入 map，Count 缺省 0 → 测试 TestRangeCompleteness。
4. 失败不留痕：api.New 在 width<=0 时返回 (nil, ErrNonPositiveWidth)，不构造任何对象 → 测试 TestNewInvalidWidth。
