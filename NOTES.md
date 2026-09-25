# NOTES — 接收窗口通告与零窗口处理

## 八步推导（w0=100；初始 una=0 next=0 wnd=100 right=100 avail=100）

| # | 操作 | una | next | wnd | right | avail | 结果 |
|---|------|-----|------|-----|-------|-------|------|
| 1 | Send(60) | 0 | 60 | 100 | 100 | 40 | 成功 |
| 2 | RecvWindow(50) | 0 | 60 | 100 | 100 | 40 | 忽略（cand=50<right=100，非零收缩） |
| 3 | Send(40) | 0 | 100 | 100 | 100 | 0 | 成功 |
| 4 | RecvAck(100) | 100 | 100 | 100 | 200 | 100 | 成功（右边缘随确认前进） |
| 5 | RecvWindow(0) | 100 | 100 | 0 | 100 | 0 | 成功（零窗口，right 收缩到 una） |
| 6 | Send(50) | 100 | 100 | 0 | 100 | 0 | ErrWindowExceeded |
| 7 | RecvWindow(80) | 100 | 100 | 80 | 180 | 80 | 成功（cand=180>=right=100） |
| 8 | Send(80) | 100 | 180 | 80 | 180 | 0 | 成功 |

- (甲) 若第 2 步接受收缩（wnd=50, right=50）：avail=max(0,50-60)=0，第 3 步 Send(40) 被错拒为 ErrWindowExceeded（正确应成功）。
- (乙) 若第 5 步把零窗口也当收缩忽略（wnd 仍 100, right=200, avail=100）：第 6 步 Send(50) 被错放行（next=150），正确应报 ErrWindowExceeded。
- (丙) 第 6 步被拒后发送方 wnd=0、right=una、avail=0。若第 7 步窗口更新丢失，发送方永远认为窗口为 0，接收方虽已消费出空闲缓冲区，发送方却无从得知 → 双方互等、死锁。故接收方必须在有空间时主动重发窗口更新，或由发送方做零窗口探测（persist），否则死锁无法打破。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：win.Window 的 AdvanceSend/ApplyAck/ApplyWindow 逐步执行规则；测试 `TestRandomSequencesMatchModel`（api_test.go，随机交错序列对比测试内朴素模型）。
2. 右边缘不收缩（零窗口例外）：win.ApplyWindow 中 cand<right 且 w>0 时直接返回不改状态；测试 `TestRightEdgeMonotone`。
3. 发送不越窗、avail>=0：flow.Send 先查 win.Sendable(n)，Avail 用 max(0,right-next)；测试 `TestSendBoundedAndAvailNonNegative`。
4. 失败不留痕：flow 三个方法先校验后落地，校验失败直接返回哨兵错误（ErrWindowExceeded/ErrBadAck/ErrBadWindow，互不相同）；测试 `TestRejectedOpsLeaveStateUnchanged`。

复杂度：win.Window 内非导出字段 checked 记录最近操作检查的在途单元数（O(1) 边界检查，恒为 1）；测试 `win.TestCheckedUnitsConstant`。
