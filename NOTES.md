# NOTES — 后缀自动机（ontology-760）

## 三、s="abcbc" 的 SAM 推导（8 状态）

构建：extend a,b,c 得状态 1,2,3（link 均→0）；第 4 字节 b 在状态 0 处冲突，分裂出克隆 5(len=1,link=0)，状态 2 的 link 改指 5；第 5 字节 c 同理分裂出克隆 7(len=2,link=0)，状态 3 的 link 改指 7。

| 状态 | len | link | len-len[link] |
|------|-----|------|---------------|
| 0 | 0 | - | 0 |
| 1 | 1 | 0 | 1 |
| 2 | 2 | 5 | 1 |
| 3 | 3 | 7 | 1 |
| 4 | 4 | 5 | 3 |
| 5 | 1 | 0 | 1 |
| 6 | 5 | 7 | 3 |
| 7 | 2 | 0 | 2 |

合计 1+1+1+3+1+3+2 = 12，与枚举子串去重一致。

- (甲) 忘减 len[link]：Σlen[v] = 1+2+3+4+1+5+2 = **18**（正确 12）。
- (乙) 终止状态只取整串后缀链 6→7→0；"b" 从根转移到状态 5，不在链上 → 错算 **0**（正确 2）。
- (丙) 无转移直接重置 (根,0)、当前字节作废：t=bcabc 轨迹 b(1)→bc(2)→a 失配重置→b(1)→bc(2)，best=**2**（正确 3，"abc"）。

## 二、四条不变量的保证位置与钉住测试

1. 与朴素一致：query.Distinct/Occurrences/LongestCommonSubstring 按定义实现 → `TestAgainstNaive`。
2. link 树结构：sam.extend 分裂时 len 严格递增、link 指向已存在状态 → `TestLinkTreeInvariants`。
3. 右端集合计数：query.Occurrences 按 len 降序把终止计数累加到 link 父 → `TestOccurrencesExact`。
4. 失败不留痕：sam.New 与各查询先校验后执行，拒绝路径无任何写 → `TestFailureLeavesNoTrace`。
