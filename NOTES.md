# ontology-604 接收窗口通告与零窗口处理

## 八步推导（w0=100；每行：操作 → una next wnd right avail → 结果）

    初始            → 0   0   100 100 100
    1 Send(60)      → 0   60  100 100 40  → 成功
    2 RecvWindow(50)→ 0   60  100 100 40  → 忽略（cand=0+50=50 < right=100，非零收缩）
    3 Send(40)      → 0   100 100 100 0   → 成功
    4 RecvAck(100)  → 100 100 100 200 100 → 成功（right 随确认前进到 una+wnd）
    5 RecvWindow(0) → 100 100 0   100 0   → 零窗口，接受（right 收缩到 una，唯一合法收缩）
    6 Send(50)      → 100 100 0   100 0   → ErrWindowExceeded（avail=0，状态不变）
    7 RecvWindow(80)→ 100 100 80  180 80  → 成功（cand=100+80=180 >= right=100）
    8 Send(80)      → 100 180 80  180 0   → 成功

(甲) 若第 2 步接受收缩（wnd=50、right=50）：第 3 步 avail=max(0,50-60)=0，Send(40) 被错拒为 ErrWindowExceeded（正确应成功发送 40）。
(乙) 若第 5 步把零窗口当收缩一并忽略（wnd 仍 100、right 仍 200）：第 6 步 avail=100，Send(50) 被错放成功（正确应报 ErrWindowExceeded），发送方会撑爆已满的接收缓冲。
(丙) 第 6 步被拒后，若第 7 步 RecvWindow(80) 丢失：发送方 wnd=0、right=una、avail 恒为 0，永久停发；接收方已消费、缓冲已空，却在等永远不来的新数据——双方互等即死锁。所以接收方腾出缓冲后必须主动重发窗口更新（或由发送方做零窗口探测/persist 定时器），否则死锁不可解。

## 四条不变量：保证位置 + 钉住测试

1. 与朴素参照一致：规则集中在 win.Window 的 CanSend/ValidAck/ApplyWindow（win/win.go）；测试 TestMatchesNaive（api/api_test.go，随机序列与朴素参照对拍）。
2. 右边缘不收缩（零窗口唯一例外）：right=una+wnd 派生，ApplyWindow 仅在 cand>=right 或 w==0 时改 wnd（win/win.go）；测试 TestRightEdgeMonotonic。
3. 发送不越窗：CanSend 要求 0<n<=Avail 且 Avail=max(0,right-next)（win/win.go）；测试 TestSendWithinWindow。
4. 失败不留痕：flow 先校验后应用，错误路径无任何写（flow/flow.go）；测试 TestRejectedOpsKeepState。
