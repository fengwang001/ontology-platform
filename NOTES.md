# NOTES

## 三、两指针归并推导（m=10；A idx=[1,3,5] val=[2,3,5]，B idx=[0,1,5] val=[4,7,6]）

| 步 | i | j | 比较 | 匹配 | 本步贡献 | 累计 |
|---|---|---|---|---|---|---|
| 1 | 0 | 0 | A1>B0 | 否（前进 j） | 0 | 0 |
| 2 | 0 | 1 | A1=B1 | 是（同进） | 2·7=14 | 14 |
| 3 | 1 | 2 | A3<B5 | 否（前进 i） | 0 | 14 |
| 4 | 2 | 2 | A5=B5 | 是（同进） | 5·6=30 | 44 |
| 5 | 3 | 3 | 双端耗尽，循环结束 | — | — | 44 |

- (甲) 上界错写成 `i < lenA-1`：i 到 2 即停，漏掉公共下标 5（贡献 30），点积错成 **14**。
- (乙) 忽略 idx 按位置对齐：2·4+3·7+5·6 = 8+21+30，点积错成 **59**。
- (丙) 不等时两指针同进：第 1 步把 B 的下标 1 跳掉（丢失 14），只剩 5·6，点积错成 **30**。

## 二、四条不变量：代码保证位置 / 钉住的测试函数

1. 与稠密参照逐位相等：归并求和在 `sdot/dot.go` 的 `Dot`；钉住：`TestDotMatchesDenseReference`、`TestSelfCheck`。
2. 规范形（idx 严格递增、val 非零、等长、下标在 [0,m)）：`spv/vec.go` 的 `Set`（sort.SearchInts 定位后的覆盖/删除/有序插入）与 `api/api.go` 的 `Build` 预校验；钉住：`TestSetCanonicalForm`。
3. 只访问非零条目且计数=lenA+lenB、不随 m 增长：`sdot/dot.go` 的原子计数器 `accessed`；钉住（白盒）：`TestAccessCountIndependentOfM`。
4. 失败不留痕（含计数器）：所有校验先于任何状态修改，Set 见 `spv/vec.go`、Build 见 `api/api.go`；钉住：`TestRejectedOperationsLeaveNoTrace`、`TestBuildRejections`。
