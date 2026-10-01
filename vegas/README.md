# Vegas 式并发限制器

`vegas` 包实现一个带 **令牌超时回收**、**丢弃降级冷却** 与 **滑动最小时延
窗口** 的 Vegas 式并发限制器。所有判定都携带 `int64` 时刻，状态机完全确定：
相同的操作序列重放得到完全相同的放行判定与上限 `L` 序列。

## 配置

`Config{L0, Lmin, Lmax, Alpha, Beta, Tmo, Cd, Wm}` 全部为整数，构造时整体
校验，任一项非法返回 `ErrInvalidConfig`：

- `Lmin >= 1`；`Lmin <= Lmax <= 10^6`；`Lmin <= L0 <= Lmax`
- `Alpha >= 0` 且 `Beta > Alpha`
- `1 <= Tmo, Wm <= 10^9`；`0 <= Cd <= 10^9`

## 状态

- `L`：当前上限，初值 `L0`，恒有 `Lmin <= L <= Lmax`
- `n`：在途请求数，恒等于未归还且未超时的令牌数
- 令牌表：序号（首个为 1，被拒的 `Acquire` 不消耗序号）、放行时的 `w`
  （放行后的 `n`）、超时时刻 `now+Tmo`，按序号（即到期时刻）升序
- 已超时集合：被 `reap` 回收的令牌序号，之后再归还报 `ErrTokenTimedOut`
- 成功样本窗口：`(at, rtt)` 序列，只保留满足 `at+Wm > now` 的样本
- `lc`：上次降级时刻（初为空）；`maxNow`：已通过前置检查的最大时刻

## 排队估计公式

成功归还时，先把 `(now, rtt)` 放入窗口，再取窗口内最小 rtt 记为 `m`。
排队估计量

```
q = ceil(L * (rtt - m) / rtt)
```

- `q < Alpha`：`L = min(Lmax, L+1)`
- `q > Beta`：`L = max(Lmin, L-1)`
- `Alpha <= q <= Beta`：不变（`q` 恰等于 `Alpha` 或 `Beta` 时都不动）
- 首个成功样本的窗口只有自身，故 `m == rtt`、`q == 0`

## 应用受限判定

若放行该请求时的在途数 `w` 满足 `w*2 < L`（用归还时刻的当前 `L` 判断），
认为应用没有把限流器压满，本次**不调整** `L`；但样本仍入窗口、`m` 照常
更新。注意 `w*2 == L` 时**不算**受限，Vegas 调整正常生效。

## 降级冷却与超时回收

- `cut(now)`：`lc` 为空或 `now >= lc+Cd` 时，
  `L = max(Lmin, floor(L*9/10))` 且 `lc = now`（即使取整后 `L` 没变也更新
  `lc`）；冷却中什么都不做。`Cd = 0` 表示每次降级都立即生效。
- `reap(now)`：按序号升序处理所有 `expires <= now`（恰等于即超时）的未归还
  令牌：每个令 `n--`、移入已超时集合，然后执行一次 `cut(now)`。一次 reap
  回收多个令牌时每个令牌各触发一次 `cut`，但冷却通常只让第一次真正降 `L`。

## 操作次序

1. `Acquire(now)` / `Release(...)` 先做三项前置检查，顺序固定且只报第一个：
   1. 仅 Release：结果不是成功/丢弃/忽略之一 → `ErrInvalidResult`
   2. `now < 0` 或 `now > 10^15` → `ErrInvalidTime`
   3. `now < maxNow` → `ErrClockRewind`
   这三类拒绝不改任何状态（连 `reap` 都不发生）。
2. 通过后 `maxNow = now` 并执行 `reap(now)`。
3. 之后的状态类拒绝保留 reap 与 `maxNow` 推进，其余不变：
   - Acquire：`n >= L` → `ErrAtLimit`（`L` 降到低于 `n` 期间一直拒绝）
   - Release：序号在已超时集合 → `ErrTokenTimedOut`；从未发放或已归还 →
     `ErrInvalidToken`；成功但 `rtt < 1` 或 `rtt > 10^12` → `ErrInvalidRTT`
     （令牌仍视为未归还，可再次归还）
4. 正常放行：`n++`，序号 +1，令牌记录 `w=n` 与 `now+Tmo`。
5. 正常归还：`n--`；忽略直接结束；丢弃再 `cut(now)`；成功入窗口并按上面的
   Vegas 公式调整（先于调整取 `m`，用当前 `L`）。

## 数据结构与复杂度

- 令牌表按序号/到期升序存放：`reap` 只从队首弹出到期项；归还用二分定位，
  单次操作不随在途数线性增长。
- 滑动窗口用「样本队列 + 单调双端队列」维护窗口最小 rtt：出窗与入窗均摊
  `O(1)`。
- 非导出计数器 `windowExamined`、`tokenExamined` 累计真正考察过的窗口项与
  令牌数；`Examined()` / `State()` 可读。它们在整体上不超过「放行令牌数 +
  成功样本数」的常数倍。
- 所有公开方法用同一把互斥锁串行化，并发调用等价于某个串行顺序。

## 本地验证

```bash
go test ./...
go test -race -v ./vegas
go test -v -run TestLoggedExample ./vegas   # 打印输入/输出/判定依据
go test -coverprofile=cov.out ./... && go tool cover -func=cov.out
```

测试包含题目完整示例、全部边界（`w*2` 等于/小于 `L`、`q` 恰为 `Alpha/Beta`、
冷却边界、`Cd=0`、超时恰相等与差 1、窗口恰在 `t+Wm` 出窗、上下限钉住、
`L<n` 后拒绝等）、`-race` 并发校验、均摊计数器在 1000 与 100000 次操作下的
常数倍检查，以及 2000 组随机操作序列与逐项线性扫描的朴素参考实现
（`naiveLimiter`）差分对照。
