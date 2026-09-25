# ontology-603 握手推导与不变量

## 八步推导（nextISN 初值 0；归属：半开{clientISN,serverISN}/已建立/无）

| # | 操作 | 返回 | nextISN | A | B | C | D |
|---|------|------|---------|---|---|---|---|
| 1 | RecvSYN(A,1000) | SYN-ACK(sISN=0, ack=1001) | 1 | 半开{1000,0} | 无 | 无 | 无 |
| 2 | RecvSYN(A,1000) | 重复SYN：SYN-ACK(0,1001)，状态不变 | 1 | 半开{1000,0} | 无 | 无 | 无 |
| 3 | RecvSYN(B,2000) | SYN-ACK(1,2001) | 2 | 半开 | 半开{2000,1} | 无 | 无 |
| 4 | RecvACK(A,1) | 完成：clientISN=1000, serverISN=0 | 2 | 已建立 | 半开 | 无 | 无 |
| 5 | RecvACK(A,1) | 重复ACK：幂等 no-op，不报错 | 2 | 已建立 | 半开 | 无 | 无 |
| 6 | RecvSYN(C,3000) | SYN-ACK(2,3001) | 3 | 已建立 | 半开 | 半开{3000,2} | 无 |
| 7 | RecvSYN(C,9999) | 替换：SYN-ACK(3,10000) | 4 | 已建立 | 半开 | 半开{9999,3} | 无 |
| 8 | RecvACK(D,999) | ErrHalfOpen | 4 | 已建立 | 半开 | 半开 | 无 |

- (甲) 若第 2 步误当新连接：回 SYN-ACK(1,1001)（多消耗 serverISN=1，nextISN=2），A 的半开被替换为 {1000,1}；第 4 步 ACK(ack=1) 对不上新的 serverISN+1=2 → ErrBadAck，A 永远完不成握手，serverISN=0 被白白浪费。
- (乙) +1 来自「SYN 消耗一个序号」。完成条件误写成 ack==serverISN：第 4 步合法 ACK(1)≠0 被判 ErrBadAck，握手卡死（且会错放 ack=0 的坏 ACK）。SYN-ACK 确认号误写成 clientISN：对端认为 SYN 未被确认而不断重传 SYN，双方对序号的期望永久错位。
- (丙) 不维护已建立集合：第 5 步被判 ErrHalfOpen，与第 8 步同错。本质区别：第 5 步是已完成连接的合法重复 ACK（丢包重传下常见），必须幂等吸收；第 8 步 D 从未握手，是真孤儿 ACK，必须报错。无已建立集合则两者不可区分，要么误杀合法重复，要么错放孤儿。

## 四条不变量落点（位置 → 钉住它的测试）

1. 与朴素参照一致：handshake.go 的 RecvSYN/RecvACK 逐规则实现 → TestModelInterleave（随机序列对拍朴素模型）。
2. 序号协商正确：hs.go 的 Alloc 单调分配 + handshake 的 +1 规则 → TestSeqNegotiation。
3. 重复幂等：RecvSYN 重传分支、RecvACK 已建立分支均不改状态 → TestDuplicateIdempotent。
4. 失败不留痕：ErrBadAck/ErrHalfOpen/ErrBadSeq 都在写状态前返回 → TestFailureNoTrace。
