# Go-Back-N 发送端推导笔记

## 十步表（W=4；「重传」列只在 Timeout 时非空）
1. Send     base=0 next=1 未确认{0}         重传无
2. Send     base=0 next=2 未确认{0,1}       重传无
3. Send     base=0 next=3 未确认{0,1,2}     重传无
4. Send     base=0 next=4 未确认{0,1,2,3}   重传无（窗口满）
5. Ack(2)   base=2 next=4 未确认{2,3}       重传无；累计释放段0,1
6. Send     base=2 next=5 未确认{2,3,4}     重传无（滑窗后发段4）
7. Send     base=2 next=6 未确认{2,3,4,5}   重传无（发段5，窗口再满）
8. Ack(1)   base=2 next=6 未确认{2,3,4,5}   重传无；1<=base，幂等 no-op
9. Ack(5)   base=5 next=6 未确认{5}         重传无；累计释放段2,3,4
10. Timeout base=5 next=6 未确认{5}         重传[5,6)={5}；base/next 不变

## (甲) 第5步
正确：base=2，段0、1 被确认释放，未确认{2,3}。若误实现为「单段确认」（ACK=a 只确认 a），段0、1 仍挂着，base 错成 0，段0、1 被错误保留为未确认，且段2 被单独抠除形成空洞，累计语义全失。

## (乙) 第8步
正确处理：1<=base 属重复/乱序 ACK，幂等忽略，base/next/未确认集合一律不变。若错写成回退 base=a：第8步后 base 错成 1，窗口计数 next-base=5 > W=4（越界）；若在回退态发生 Timeout，将按 [1,6) 错误重传 {1,2,3,4,5}，其中段1 早已被 Ack(2) 释放。本题序列中第9步 Ack(5) 会先把 base 拉回 5，故实际第10步仍只重传 {5}；危害在于回退态一旦插入 Timeout 即误重传已释放段，且窗口占满判定失真。

## (丙) 重传集合依赖时序（发送 0,1,2,3 后，base=0 next=4）
顺序甲：先 Timeout 后 Ack(3) → 重传 {0,1,2,3}，终态 base=3 未确认{3}。顺序乙：先 Ack(3) 后 Timeout → 重传 {3}，终态同为 base=3 未确认{3}。终 base 相同（=ACK 最大值，与顺序无关）而重传集合不同；差异来自「ACK 立即移除 seq<base 的段、base 只进不退」叠加「超时重传区间恒为 [base,next)」——区间下界被 ACK 抬高后，已覆盖段不再参与重传。

## 四条不变量的保证位置与钉住测试
1. 与朴素参照一致：gbn.go 的 Ack 调 snd.Window.Advance（base=max(base,a)），未确认集合恒等于环上落在 [base,next) 的槽——TestNaiveReference（随机交错对拍朴素模型）。
2. 窗口不越界：snd.go 的 Send 先判 next-base>=W 即返回 ErrWindowFull，且不写槽不增 next——TestWindowBound。
3. ACK 单调且幂等：snd.go 的 Advance 开头 a<=base 直接返回、不碰任何状态——TestAckMonotonicIdempotent。
4. 失败不留痕：gbn.go 的 Ack 在改状态前先拦截负数；snd.go 的 New/Send 均校验先于写入——TestRejectedOpsLeaveNoTrace。
附：推进检查计数 advanceChecks 是 snd 的非导出字段（推进只挪指针，恒为 0），仅白盒 TestAdvanceChecksConstant 直接读字段断言 m=100..10000 不增长，任何导出路径都读不到它。
