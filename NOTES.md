# NOTES — 等宽直方图增量维护

## 第三节推导：六行分步表（width=10, anchor=0, k=floor(v/10)）

| 步 | v   | k  | 桶区间     | 本步后各桶计数（minBucket..maxBucket） |
|----|-----|----|------------|----------------------------------------|
| 1  | 5   | 0  | [0,10)     | 0:1                                    |
| 2  | 12  | 1  | [10,20)    | 0:1 1:1                                |
| 3  | 20  | 2  | [20,30)    | 0:1 1:1 2:1                            |
| 4  | 10  | 1  | [10,20)    | 0:1 1:2 2:1                            |
| 5  | -3  | -1 | [-10,0)    | -1:1 0:1 1:2 2:1                       |
| 6  | -15 | -2 | [-20,-10)  | -2:1 -1:1 0:1 1:2 2:1                  |

- (甲) 第 5 步 `-3` 正确桶号是 **-1**（floor(-3/10)=-1）。若用 Go 截断除法 `-3/10=0`，会错入桶 **0**：`BucketCount(-1)` 错成 **0**（应 1），`BucketCount(0)` 错成 **2**（应 1）。
- (乙) 第 3 步 `20` 是桶 1 右边界，半开区间下正确入桶 **2**。若按闭区间误判，会错入桶 **1**：`BucketCount(1)` 错成 **2**（应 1），`BucketCount(2)` 错成 **0**（应 1）。
- (丙) 固定数组只覆盖 `[0,30)` 并静默丢弃越界值时，**-3 与 -15** 被丢，`Total()` 错成 **4**（应 **6**）。

## 四条不变量：保证位置 → 钉住它的测试

1. 与批量重算一致：`hist.Hist.Insert` 用 `bucket.Number`（数学向下取整）定位后 `count[k]++`（hist/hist.go）→ `TestBatchEquivalence`
2. 计数守恒：`total++` 与 `count[k]++` 在同一把写锁内完成，无丢弃分支（hist/hist.go Insert）→ `TestTotalConservation`
3. 范围完整：`Insert` 仅在 k 越界时扩 `min/max`，范围外桶永无计数（hist/hist.go Insert/Range）→ `TestRangeCompleteness`
4. 失败不留痕：`api.New` 先校验 `width<=0`，返回 `ErrNonPositiveWidth` 且实例为 nil（api/api.go New）→ `TestNewInvalidWidth`
