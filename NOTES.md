# Bloom filter 推导笔记（m=10, k=3）

公式：h1=(Σb) mod m；h2=1+((Σb_i·(i+1)) mod (m-1))；pos_j=(h1+j·h2) mod m。
单字节串：h1=b mod 10，h2=1+(b mod 9)。

## 八行分步表（“为1的位”写该操作后的全集；Test 不改位）

| 操作 | h1 | h2 | pos_0..2 | 操作后为1的位 | Test |
|---|---|---|---|---|---|
| Add("a") | 7 | 8 | 7,5,3 | {3,5,7} | — |
| Add("b") | 8 | 9 | 8,7,6 | {3,5,6,7,8} | — |
| Add("c") | 9 | 1 | 9,0,1 | {0,1,3,5,6,7,8,9} | — |
| Test("o") | 1 | 4 | 1,5,9 | 不变 | true（假阳性：1←c,5←a,9←c） |
| Test("a") | 7 | 8 | 7,5,3 | 不变 | true |
| Test("d") | 0 | 2 | 0,2,4 | 不变 | false |
| Test("b") | 8 | 9 | 8,7,6 | 不变 | true |
| Test("x") | 0 | 4 | 0,4,8 | 不变 | false |

## 三问

- (甲) Test("d")=false：正确实现查位 0,2,4，其中 2、4 为 0。若错用 k=1（只查 h1→位 0），位 0 已被 "c" 置 1，会错成 true（假阳性）。正确依据位 {0,2,4}，错误依据位 {0}。
- (乙) 删 "a" 清位 {7,5,3} 后，Test("b") 查 {8,7,6}：位 7 被清 → false。已插入的 "b" 测出 false，违反不变量 1（无假阴性）。
- (丙) 漏掉 mod m 时 Add("a") 的 j=2 位置 = 7+2·8 = 23，越出位数组下标 0..9：直接 index out of range（panic），Add 崩溃无法完成；若数组恰好更大则静默写错位，随后 Test("a") 假阴性。

## 四条不变量的保证位置与钉住测试

1. 无假阴性：bloom.Filter.Add 在锁内置齐 k 位才计数（bloom.go Add），Test 查同一组 hashk.Positions。测试：TestNoFalseNegative、TestSelfCheck。
2. 与朴素参照一致：Add/Test 只经 hashk.Positions 落位（bloom.go），无第二份位逻辑。测试：TestNaiveReference、TestEightOpsGolden。
3. 假阳性唯一来源：bits 的唯一写路径是 Add 的置位（bloom.go），无隐藏状态。测试：TestFalsePositiveSource。
4. 失败不留痕：参数/空元素/容量校验全部在写状态之前 return（api.go New/Add、 bloom.go New/Add）。测试：TestRejectedNoMutation、TestSentinelErrors。
