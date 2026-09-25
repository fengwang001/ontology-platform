# 规范 Huffman：推导与不变量
## 合并表（freq A5 B2 C1 D1 E1；并列取名字小者，合成名=子树最小符号）
| 步 | 合并两节点 | 合成频次(名) |
|---|---|---|
| 1 | C(1)+D(1) | 2 (C) |
| 2 | E(1)+B(2)（2 并列 B<CD） | 3 (B) |
| 3 | CD(2)+BE(3) | 5 (B) |
| 4 | A(5)+CDBE(5)（并列 A<B） | 10 (A) |
码长=深度：A=1，B=C=D=E=3。
## 规范码表（按码长升序、符号升序；code 移位后递增）
| 符号 | 码长 | 码字 |
|---|---|---|
| A | 1 | 0 |
| B | 3 | 100 |
| C | 3 | 101 |
| D | 3 | 110 |
| E | 3 | 111 |
"AABCCD" → 0 0 100 101 101 110 = `00100101101110`（14 位）→ 字节 `0x25 0xB8`。

- (甲) 树左0右1：A=0 B=110 C=100 D=101 E=111；规范码 B←树C的100、C←树D的101、D←树B的110，三者循环互换，E 不变。
- (乙) 把 B 的 100 误读成 10：AA 后 "10"→B，余 `0101101110` 正解为 A C C D，得 **AABACCD**（7 符号）。前缀自由 ⇒ 切分唯一，正确解码器无歧义。
- (丙) `Encode([])`→空切片；`{'A':1}` 码为 `0`（1 位），`Encode("AA")`=2 位 `00`→`0x00`；`{'A':0,'B':0}`→`ErrInvalidFreq`。
## 不变量（保证位置 / 钉住它的测试）
1. 与朴素参照一致：htree.Lengths 逐步合并最小对（htree/htree.go），hcode.Encode 大端打包、Decode 逐位读码（hcode/hcode.go）；TestLengthsAndCodes、TestEncodeNaive、TestDecodeNaive。
2. 切分点无关：hcode.Stream.Feed 位缓冲逐位消费（hcode/hcode.go）；TestFeedChunkings。
3. 确定性/前缀自由：Build 排序后区间分配（hcode/hcode.go）；TestDeterministic、TestPrefixFree。
4. 失败不留痕：错误路径先校验、返回 nil，不写状态（api/api.go、hcode/hcode.go）；TestFailureAtomic。
