# NOTES

## 七步分步表（actor id∈{0,1,2}，缺失 key 视为 0）

| 步 | 操作 | 返回值 |
|---|---|---|
| S1 | Set("r1", {0:1}) | OK(nil) |
| S2 | Set("r2", {0:1, 1:2}) | OK(nil) |
| S3 | Set("r3", {1:1, 2:1}) | OK(nil) |
| S4 | Merge("r1", "r2") | {0:1, 1:2} |
| S5 | Merge("r2", "r3") | {0:1, 1:2, 2:1} |
| S6 | Compare("r1", "r2") | Less |
| S7 | Compare("r1", "r3") | Concurrent |

- (甲) S4 正确结果 {0:1,1:2}。若误写成逐 key 取 min：并集上 key0=min(1,1)=1，key1=min(0,2)=0，得 {0:1,1:0}（稀疏表示即 {0:1}），**actor 1 的计数 2 丢失**。
- (乙) S5 正确结果 {0:1,1:2,2:1}。若只遍历 r2 的 key 得 {0:1,1:2}，**漏掉仅出现在 r3 一侧的 actor 2 的计数 1**（key0 来自 r2 一侧，不会丢）。
- (丙) S6 正确结果 **Less**（共有 key0 相等 1==1，但 key1 上 0<2）。若只比较两边共有 key（仅 key0），会因 1==1 而**错判成 Equal**。S7 Compare("r1","r3") 正确结果 **Concurrent**：key0 上 1>0（r1 领先），key1 上 0<1，key2 上 0<1（r3 领先），两侧各有严格大的 key。

## 四条不变量的保证位置与钉住测试

1. 与朴素重算一致：`vv.Merge`（vv/vv.go，逐并集 key 取 max）与朴素枚举对拍，测试 `TestMergeMatchesNaive`（vv/vv_test.go）。
2. 合并是上确界：`vv.Merge`（vv/vv.go），交换律/幂等/结果 >= 两输入，测试 `TestMergeJoinLaws`。
3. 比较三歧正确：`vv.Compare`（vv/vv.go）与逐 key 的 <=/>= 朴素定义对拍，测试 `TestCompareTrichotomy`（表驱动用例在 `TestCompareTable`）。
4. 失败不留痕：`reg.Registry.Set` 先全量校验通过后才深拷贝写入、`Merge/Compare` 只做 RLock 只读查找（reg/reg.go），三类哨兵错误互不相同；测试 `TestRejectedOpsLeaveNoTrace` 与 `TestSentinelErrorsDistinct`（api/api_test.go）。`TestSelfCheck` 对内置操作序列复核全部四条。
