# 租约与心跳保活 NOTES

## 七步推导（TTL=10，名字 "L"）

| 步 | 操作 | token | owner | expiry | 结果 |
|---|---|---|---|---|---|
| S1 | Acquire("L","A",0) | 1 | A | 10 | 授权，发 token=1 |
| S2 | Renew("L",1,5) | 1 | A | 15 | 续期（5<10）→ 5+10 |
| S3 | Renew("L",1,12) | 1 | A | 22 | 续期（12<15）→ 12+10 |
| S4 | Acquire("L","B",20) | 2 | B | 30 | 授权，token 严格递增为 2 |
| S5 | Renew("L",1,21) | 2 | B | 30 | 拒绝 ErrStaleToken，状态不变 |
| S6 | Renew("L",2,31) | 2 | B | 30 | 拒绝 ErrExpired，状态不变 |
| S7 | Expired("L",30) | 2 | B | 30 | true（now==expiry，左闭即到期）|

(甲) S6：now=31 ≥ expiry=30，正确实现拒绝（ErrExpired），expiry 保持 30。若续期不看是否到期、无条件延长，expiry 会错成 31+10=41，已死租约被复活。
(乙) S5：token=1 是 A 的旧令牌，当前为 2，正确实现拒绝（ErrStaleToken）。若只查 now<expiry 不查 token，A 的旧 token=1 会续期成功（expiry 错成 21+10=31），A、B 两个持锁者并存，栅栏失效、脑裂。
(丙) S7：now==expiry 左闭，正确结果是 true（已到期）。若写成 now>expiry，此刻错判成 false（未到期）。expiry 是租约失效的第一个时刻，边界必须左闭，否则旧持锁者在到期瞬间多活一个时间单位，与新持锁者重叠。

## 四条不变量：保证位置 + 钉住它的测试

1. 与朴素重算一致：`lease/lease.go` 的 `Expired` 逐字 `now >= l.expiry`；测试 `TestExpiredMatchesNaive`（api_test，随机操作序列对拍朴素模型）。
2. 栅栏单调：`mgr.Acquire` 只在旧 token 上 +1，`lease.Renew` 先比 token 再动状态；测试 `TestFencingMonotonic`（mgr_test）。
3. 续期只在存活期：`lease.Renew` 中 `now >= l.expiry → ErrExpired`，先于写 expiry 返回；测试 `TestRenewOnlyWhileAlive`（mgr_test）。
4. 失败不留痕：`mgr` 各方法先校验（空名/未授予/token/到期）后改写，拒绝路径在任何写之前返回；测试 `TestFailureLeavesNoTrace`（api_test）。
