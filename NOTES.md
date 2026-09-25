# Ricart–Agrawala 互斥：推导与不变量

内置序列：P2 Request(3)、P1 Request(5)、P3 Request(5)；全序 (3,2)<(5,1)<(5,3)。七行表（X 收到 Y 的 REQUEST）：

1. P1←P2：OK，P1 此时不感兴趣，立即许可。
2. P3←P2：OK，P3 此时不感兴趣，立即许可。
3. P2←P1：defer，X=P2 的 (3,2) 小于 (5,1)，X 更优先，延迟。
4. P3←P1：OK，P3 此时不感兴趣，立即许可。
5. P2←P3：defer，(3,2) 小于 (5,3)，X 更优先。
6. P1←P3：defer，(5,1) 小于 (5,3)，X 更优先（ts 相同比 pid，P1 优先）。
7. P2 收齐 P1、P3 的 OK 先入；退出放行延迟集 {P1,P3}→P1 收齐先入；P1 退出放行 {P3}→P3 入。进入顺序 P2→P1→P3。

（甲）并列错成「pid 大者优先」：P1 对 P3 反给 OK、P3 反而 defer P1；P2 退出后 P3 先于 P1 进入，P1 等到 P3 退出才进，顺序变成 P2→P3→P1，P1 被 P3 插队。
（乙）「不感兴趣也 defer」：N=2，仅 P1 Request(1)，空闲的 P2 一律 defer，P1 一个 OK 都收不到——感兴趣者存在却没有可进入者（三进程例中 P2 首个广播就被空闲的 P1、P3 defer，随后全体锁死）。
（丙）退出漏放行：P1、P3 永远收不到 P2 的 OK，P2 离开后仍无进程可进入，P1/P3 饥饿；破坏不变量 3（无死锁），不变量 2 的顺序也无法兑现。

不变量（保证位置 / 钉住的测试函数）：
1 互斥：`ra.(*System).Enterable` 仅在 okCount==interested-1 时为真，api 同名方法共用同一把锁；TestMutex_AtMostOneEnterable（并发侧 TestConcurrent_OrderAndSingleEnterable）。
2 全序一致：`ra.Resolve` 对每个有序对调用 ord.Less 判 defer/OK，全序最小者收齐全部 OK，`ra.Leave` 退出时放行并清空延迟集；TestEntryOrder_MatchesTotalOrder。
3 无死锁：全序最小者被其余感兴趣者一律回 OK（ord.Less 的三歧性），Resolve 后 okCount 必达标；TestNoDeadlock_AlwaysEnterable。
4 失败不留痕：`api.Request/Exit/Enterable` 全部先做哨兵校验、通过后才改状态；TestRejectedOps_LeaveStateUntouched。
附：收齐 OK 用 proc.okCount 做 O(1) 计数判定，检查条数记在非导出的 ra.lastCheck；TestOKCollectionCheckConstant 同包直接读字段，断言各档 m 下恒为常数、不随 m 线性增长。
