# NOTES：ticket 自旋退避锁（New(大上限)，线程 A/B/C）

## 三、8 步分步表

| 步 | 操作 | next | serving | 持有者 | 正在自旋的号 |
|---|---|---|---|---|---|
| 1 | A Acquire | 1 | 0 | A(0) | — |
| 2 | B Acquire | 2 | 0 | A(0) | B(1) |
| 3 | C Acquire | 3 | 0 | A(0) | B(1),C(2) |
| 4 | A Release(0) | 3 | 1 | 交接中（等待 B 观察） | B(1),C(2) |
| 5 | B 获锁 | 3 | 1 | B(1) | C(2) |
| 6 | B Release(1) | 3 | 2 | 交接中（等待 C 观察） | C(2) |
| 7 | C 获锁 | 3 | 2 | C(2) | — |
| 8 | C Release(2) | 3 | 3 | — | — |

(甲) 正确实现授予 **B（号 1）**，严格 FIFO。单标志 test-and-set 不取号：A 清标志后 B、C 谁的 CAS 先成功谁获锁；若调度让 C 的 CAS 先执行，授予顺序变成 **A→C→B**——C 在临界区时 B 仍在自旋，晚到者先入，即乱序。
(乙) 不校验 `ticket==serving`：误调 Release(5) 直接 serving++，0→1；A 并未释放而 B 见到 serving==1 进入临界区，**A、B 同时持锁（互斥被破坏）**，C 在 serving=1 自旋，号 0 被锁遗忘，可致永久自旋/后续号错乱。正确实现：Release(5) 返回 **ErrWrongTicket**，serving 保持 0，B、C 继续自旋，只有 Release(0) 能交接。
(丙) 暂停 2^k 用有符号 int64 存：第 63 次失败后 2^63 溢出为 **-9223372036854775808**（ns；time.Sleep 负值立即返回）；再翻倍变成 0 并保持为 0，退避失效，**等价于完全不暂停的纯忙自旋**，缓存争用不再缓解。本实现翻倍后若 ≤0 或超过上限即封到 maxBackoff。

## 二、四条不变量：保证位置与钉测

1. 与朴素参照一致：`tick/tick.go` Handover 用 CAS 只把 serving 推进 1；`api/api.go` SelfCheck 跑内置八步，`api/api_test.go` TestReferenceAgreement 用 sync.Mutex FIFO 参照模型对随机脚本逐次比对授予顺序。
2. 互斥：`spin/spin.go` wait 仅在原子读 IsServing(t) 成立时返回；`api/api_test.go` TestConcurrentMutualExclusion（N=10/100/1000，临界区内 inCS 恒为 1，终值=N）。
3. FIFO 公平：`tick/tick.go` Take 的 next.Add 先到先得发号、Handover 只 +1 不跳号；`api/api_test.go` TestReferenceAgreement 逐次断言授予号恰为 0..n-1（严格按号、与 sync.Mutex FIFO 参照一致）钉住。
4. 失败不留痕：`tick/tick.go` Handover 号不符即返回不写、TryTakeFree 仅在 next==serving 时 CAS 发号；`api/api_test.go` TestRejectedOpsLeaveNoTrace（三类拒绝前后 next/serving 零变化）。O(1) 交接另由 `tick/tick_test.go` TestHandoverAccessWordsO1（m=100/1000/10000，访问状态字数≤2）钉住。
