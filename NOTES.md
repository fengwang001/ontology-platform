# Bloom filter 推导与不变量

## 八行分步表（m=10, k=3；位下标 0..9）

| 操作 | h1 | h2 | 位置 j=0,1,2 | 操作后为 1 的位 | Test 结果 |
|---|---|---|---|---|---|
| Add("a")  | 7 | 8 | 7,5,3 | {3,5,7} | — |
| Add("b")  | 8 | 9 | 8,7,6 | {3,5,6,7,8} | — |
| Add("c")  | 9 | 1 | 9,0,1 | {0,1,3,5,6,7,8,9} | — |
| Test("o") | 1 | 4 | 1,5,9 | 同上 | true（假阳性） |
| Test("a") | 7 | 8 | 7,5,3 | 同上 | true |
| Test("d") | 0 | 2 | 0,2,4 | 同上 | false |
| Test("b") | 8 | 9 | 8,7,6 | 同上 | true |
| Test("x") | 0 | 4 | 0,4,8 | 同上 | false |

- (甲) `Test("d")` 返回 **false**：正确依据是 d 的位 0,2,4 中 **2 和 4 为 0**。若错误地只用 k=1（仅 h1，位 0），位 0 已被 "c" 置 1，会错成 **true**——错误依据仅是位 0。
- (乙) 删 "a" 清位 {3,5,7}，其中位 7 与 "b" 共享，`Test("b")` 查 8,7,6 会因位 7 为 0 得 **false**。违反不变量 1（无假阴性）。
- (丙) 漏掉 `mod m`：`Add("a")` 的 j=2 位置 = 7+2·8 = **23**，越出 0..9，越界 panic（或写入错位置），位 3 置不上，随后 `Test("a")` 假阴性/程序崩溃。

## 四条不变量（保证位置 / 钉住测试）

1. 无假阴性：`bloom.Add` 在计数前把 k 个位全部置 1（bloom/bloom.go `Add`）；测试 `TestNoFalseNegative`。
2. 与朴素参照一致：`bloom.Test` 仅当 k 位全为 1 才返回 true（bloom/bloom.go `Test`）；测试 `TestNaiveReference`。
3. 假阳性受控：`Test` 只读位数组，无任何隐藏状态（bloom/bloom.go `Test`）；测试 `TestFalsePositiveControlled`。
4. 失败不留痕：`bloom` 构造与 `Add` 先校验后改状态，被拒操作零副作用（bloom/bloom.go）；测试 `TestFailureNoTrace`。
