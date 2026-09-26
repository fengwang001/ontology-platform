# NOTES — Booth 双指针推导（s="baabaa"）

d=s+s="baabaabaabaa"；n=6；初始 (i,j,k)=(0,1,0)；'>' 淘汰 i，'<' 淘汰 j，'=' 则 k++。

| 行 | 比较（d 的下标） | 结果 | 指针更新 |
|---|---|---|---|
| 1 | d[0]='b' vs d[1]='a' | > | i←0+0+1=1，i≤j 故 i=j+1=2；k=0 |
| 2 | d[2]='a' vs d[1]='a' | = | k=1 |
| 3 | d[3]='b' vs d[2]='a' | > | i←2+1+1=4（>j，不再调整）；k=0 |
| 4 | d[4]='a' vs d[1]='a' | = | k=1 |
| 5 | d[5]='a' vs d[2]='a' | = | k=2 |
| 6 | d[6]='b' vs d[3]='b' | = | k=3 |
| 7 | d[7]='a' vs d[4]='a' | = | k=4 |
| 8 | d[8]='a' vs d[5]='a' | = | k=5 |
| 9 | d[9]='b' vs d[6]='b' | = | k=6=n，结束 |

终态 (i,j,k)=(4,1,6)，答 min(i,j)=**1**，旋转 d[1:7]=s[1:]+s[:1]="aabaab"。

**(甲)** "aaaa" 四个旋转全等 "aaaa"；若改取最大起始下标，返回 **3**（正确 0）。

**(乙)** 错成最小后缀（不环绕）：后缀 k=5 的 "a" 是 k=4 "aa"、k=1 "aabaa" 的前缀，故最小后缀在 **5**（正确 1，最小旋转 "aabaab"）。

**(丙)** 错位输出 s[k+1:]+s[:k+1]=s[2:]+s[:2]="abaa"+"ba"=**"abaaba"**（正确 "aabaab"）。

## 不变量落位

1. 与朴素一致：`cyc/cyc.go` 的 `(*boothState).run`；由 **TestMinRotationMatchesNaive** 钉住。
2. 旋转真实：`cyc/cyc.go` 的 `Rotate`（api 层 `(*String).Rotate` 先做越界判定）；由 **TestRotateIsExactAndConsistent** 钉住。
3. 循环等价正确：`cmp/cmp.go` 的 `CyclicEqual`（先判等长，再比各自最小表示）；由 **TestCyclicEqual** 钉住。
4. 失败不留痕：`api/api.go` 的 `New` 先校验后构造、`Rotate` 越界先返回错误且实例构造后不可变；由 **TestRejectedOpsLeaveNoTrace** 钉住。
