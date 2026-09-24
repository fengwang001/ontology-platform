# 位点↔时间戳双向索引 NOTES

## 第三节推导（maxRecords=8，依次 Append 2,1,8,3,4,9,5,7）

| 步 | off | ts | pm |
|----|-----|----|----|
| 1 | 0 | 2 | 2 |
| 2 | 1 | 1 | 2 |
| 3 | 2 | 8 | 8 |
| 4 | 3 | 3 | 8 |
| 5 | 4 | 4 | 8 |
| 6 | 5 | 9 | 9 |
| 7 | 6 | 5 | 9 |
| 8 | 7 | 7 | 9 |

pm = [2,2,8,8,8,9,9,9]，非递减成立。

- (甲) `SafeOff(7)` 正确 = **1**（pm[1]=2<=7，pm[2]=8>7）。错解「最后一条 ts<=T 的位点」= **7**（ts[7]=7<=7，但前缀里已有 8、9，违反「前缀中不能有更晚记录」）。
- (乙) `SafeOff(8)` 正确 = **4**（pm[4]=8<=8，pm[5]=9>8）。若前缀聚合错用最小值，pmin=[2,1,1,1,1,1,1,1] 全 <=8，二分错得 **7**。
- (丙) `SafeOff(2)`：pm[0]=2 恰等于 T，用 `<=` 计入，正确 = **1**。若写成严格 `<`，pm[0]=2 不满足，错得 **-1 / found=false**。空日志（N==0）时 `SafeOff` 报 `ErrEmpty` 错误。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 前向正确：`bidx.Append` 按位点顺序落 `ts`、`TSAt` 按下标直取 → `TestForwardExact`
2. 与朴素参照一致：`bidx.SafeOff` 在非递减 pm 上二分 → `TestSafeOffMatchesNaive`
3. pm 非递减：`tsl.NextPM` 增量取 max → `TestPrefixMaxMonotone`
4. 失败不留痕：`Append`/`TSAt`/`SafeOff` 全部先校验后写，拒绝路径零修改 → `TestRejectedOpsLeaveState`
