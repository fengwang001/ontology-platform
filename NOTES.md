# NOTES

## 第三节：八步推导（nextISN 初值 0；半=半开、立=已建立，括号为 (clientISN,serverISN)）

| # | 操作 | 返回 | nextISN | A | B | C | D |
|---|---|---|---|---|---|---|---|
| 1 | SYN(A,1000) | SYN-ACK(0,1001) | 1 | 半(1000,0) | 无 | 无 | 无 |
| 2 | SYN(A,1000) | SYN-ACK(0,1001) | 1 | 半(1000,0) | 无 | 无 | 无 |
| 3 | SYN(B,2000) | SYN-ACK(1,2001) | 2 | 半 | 半(2000,1) | 无 | 无 |
| 4 | ACK(A,1) | 完成(1000,0) | 2 | 立(1000,0) | 半 | 无 | 无 |
| 5 | ACK(A,1) | no-op（不报错） | 2 | 立 | 半 | 无 | 无 |
| 6 | SYN(C,3000) | SYN-ACK(2,3001) | 3 | 立 | 半 | 半(3000,2) | 无 |
| 7 | SYN(C,9999) | SYN-ACK(3,10000) | 4 | 立 | 半 | 半(9999,3) | 无 |
| 8 | ACK(D,999) | ErrHalfOpen | 4 | 立 | 半 | 半(9999,3) | 无 |

（甲）第 2 步若一律当新连接：回 SYN-ACK(1,1001)、nextISN=2，A 的半开被换成 serverISN=1；第 4 步合法 ACK("A",1) 需满足 1==serverISN+1=2，失败 → ErrBadAck，A 永远建不起来，后续序号整体错位。
（乙）+1 来自 SYN 标志自身消耗一个序号，下一个期望字节才是 ISN+1。完成条件误写成 ack==serverISN：第 4 步 1≠0 → ErrBadAck，合法 ACK 被拒。SYN-ACK.ack 误写成 clientISN(1000)：客户端等的确认是 1001，收到 1000 视为无效/陈旧确认，持续重传 SYN，握手卡死。
（丙）不维护已建立集合：第 5 步 A 完成后即被遗忘 → 合法重复 ACK 被误报 ErrHalfOpen；第 8 步 D 从未发过 SYN，报 ErrHalfOpen 才正确。本质区别：A 完成过握手（活连接上的幂等重复），D 从未存在（真正的孤儿 ACK），只有保留已建立集合才能区分。

## 第二节：四条不变量的保证位置与钉住测试

1. 与朴素参照一致：handshake/handshake.go 的 RecvSYN（新建/重传/替换）、RecvACK（完成/重复/半开/坏ack）各分支逐条对应规则；由 TestReferenceModel 钉住。
2. 序号协商正确：hs/hs.go 的 Open 用 nextISN 单调分配 serverISN 且 SYN-ACK.ack=seq+1，完成判定 ack==serverISN+1；由 TestSequenceNegotiation 钉住。
3. 重复幂等：handshake.go 重传分支查表原样返回、不分配不写入，已建立 src 的 ACK 直接返回 nil；由 TestIdempotency 钉住。
4. 失败不留痕：api/api.go RecvSYN 在任何状态操作前先拒负序号，handshake.go 的 ErrBadAck/ErrHalfOpen 均在写入前返回；由 TestRejectedOpsLeaveNoTrace 钉住。
