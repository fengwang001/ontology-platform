# 规范 Huffman：推导与不变量对照

## 合并表（freq {A:5,B:2,C:1,D:1,E:1}，并列取字典序更小者）
| 步 | 合并两节点 | 合成节点 |
| 1 | C(1) + D(1) | CD(2) |
| 2 | E(1) + B(2) | BE(3) |
| 3 | CD(2) + BE(3) | BCDE(5) |
| 4 | A(5) + BCDE(5) | 根(10) |
码长=深度：A=1；B、E 在 BE 子树，C、D 在 CD 子树，故 B=C=D=E=3。

## 规范码表（按码长升序、符号字典序；code 移位后递增）
| 符号 | 码长 | 码字 |
| A | 1 | 0 |
| B | 3 | 100 |
| C | 3 | 101 |
| D | 3 | 110 |
| E | 3 | 111 |

"AABCCD" → 0,0,100,101,101,110 → 位串 `00100101101110`（14 位）→ 补 0 打包为 **0x25 0xB8**。

## 三问
- (甲) 左0右1（合并时小者居左）：A=0, B=111, C=100, D=101, E=110。对照规范码：B/C/D/E 循环顺移——规范(B)=树(C)=100、规范(C)=树(D)=101、规范(D)=树(E)=110、规范(E)=树(B)=111，A 不变。
- (乙) 把 B 的 `100` 误读成 `10`（错表 {0:A, 10:B, 101:C, 110:D, 111:E} 贪心解码）：0,0,10,0,10,110,111,0 → **"AABABDEA"**（8 个符号，恰好耗尽 14 位）。错表不再前缀自由（`10` 是 `101` 的前缀）故切分漂移；正确码表前缀自由 ⇒ 位流任一位置至多一个码字完整匹配，只会在真实码字边界切分，歧义无从产生。
- (丙) `Encode([])` → 空切片（0 字节）。`{'A':1}`：码长 1、码字 `0`，`Encode("AA")` = 两位 `00` → 1 字节 0x00。`{'A':0,'B':0}` 全零 → `ErrInvalidFreq`。

## 不变量 → 保证位置 → 钉住它的测试
1. 朴素一致：htree.Lengths 逐次合并最小对；hcode.New 规范赋码、Encode 大端打包 → TestTreeLengthsNaive / TestCanonicalMatchesNaive / TestEncodeMatchesNaive
2. 切分点无关：api.Stream.Feed 只累积字节，Decode 对累积缓冲一次性解析 → TestFeedChunking
3. 确定性/前缀自由：htree 并列按字典序打破 + hcode 规范码天然前缀自由 → TestDeterministicPrefixFree
4. 失败不留痕：New/Encode/Decode 出错即返回 nil，码表构建后只读 → TestFailureAtomicity
