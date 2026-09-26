# NOTES

## 第三节推导：s="baabaa" 的 Booth 双指针分步表（n=6，ss=s+s="baabaabaabaa"）

| 步 | (i,j,k) | 比较 ss[i+k] vs ss[j+k] | 结果 | 更新 |
|---|---|---|---|---|
| 1 | (0,1,0) | ss[0]='b' vs ss[1]='a' | b>a | i=1,k=0；i==j 故 j=2 |
| 2 | (1,2,0) | ss[1]='a' vs ss[2]='a' | 相等 | k=1 |
| 3 | (1,2,1) | ss[2]='a' vs ss[3]='b' | a<b | j=j+k+1=4,k=0 |
| 4 | (1,4,0) | ss[1]='a' vs ss[4]='a' | 相等 | k=1 |
| 5 | (1,4,1) | ss[2]='a' vs ss[5]='a' | 相等 | k=2 |
| 6 | (1,4,2) | ss[3]='b' vs ss[6]='b' | 相等 | k=3 |
| 7 | (1,4,3) | ss[4]='a' vs ss[7]='a' | 相等 | k=4 |
| 8 | (1,4,4) | ss[5]='a' vs ss[8]='a' | 相等 | k=5 |
| 9 | (1,4,5) | ss[6]='b' vs ss[9]='b' | 相等 | k=6=n，循环结束，答案 min(i,j)=1 |

- (甲) 相等时取最大下标：s="aaaa" 四个旋转全等，会返回 3（正确 0）。
- (乙) 错当成最小后缀（不环绕）：后缀 "a"(k=5) 是其余所有 'a' 开头后缀的前缀，字典序最小，会返回 5（正确 1，最小旋转 "aabaab"）。
- (丙) 输出错位 s[k+1:]+s[:k+1]：k=1 时输出 s[2:]+s[:2]="abaa"+"ba"="abaaba"（正确 "aabaab"）。

## 第二节四条不变量：保证位置与钉住它的测试

1. 与朴素一致：`cyc/cyc.go` 的 `MinRotation` 即 Booth 算法；测试 `cyc/cyc_test.go:TestMinRotationMatchesNaive`（表驱动+随机循环，与 O(n²) 朴素逐串比对）。
2. 旋转真实：`cyc/cyc.go` 的 `Rotate` 返回 `s[k:]+s[:k]`；测试 `cyc/cyc_test.go:TestRotateConsistent`（Rotate(MinRotation) 即最小表示）。
3. 循环等价正确：`cmp/cmp.go` 的 `CyclicEqual` 先判等长再比最小表示；测试 `api/api_test.go:TestCyclicEqual`（含不等长返回 false）。
4. 失败不留痕：`api/api.go` 的 `New`/`Rotate` 先校验后动作，哨兵错误 `ErrEmpty/ErrTooLong/ErrIndex`；测试 `api/api_test.go:TestRejectionLeavesStateIntact`（被拒后实例行为不变）。

复杂度：`cyc` 内非导出计数器 `cmpCount`（atomic，不进公开接口）；测试 `cyc/cyc_test.go:TestComparisonCountIsLinear`（同包内读，断言 ≤3n）。并发：`api` 实例构建后只读；测试 `api/api_test.go:TestConcurrentReads`。

备注：题面第十节称 `CyclicEqual("aabaa","baaab")` 为 true，但两串字符多重集不同（4a1b vs 3a2b），按题面自己给定的循环等价定义必为 false；实现与 demo 均按定义给出 false 并保留说明。
