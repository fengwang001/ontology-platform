# 分层时间轮调度器 — 设计

## 模型
- 时间为离散 tick，完全外部注入：构造注入 `start`，之后仅 `Advance(n)` 推进；代码不出现 `time.Now/After/AfterFunc/Sleep`。
- 层 `L` 的轮：`S` 个槽，槽宽 `tick_L = S^L`，量程 `interval_L = S^(L+1)`，`currentTime_L = now` 向下对齐到 `tick_L`。
- 期限 `deadline = now + delay`（以设定该延迟的操作时刻为原点）。触发判定：`now >= deadline`。

## 落槽公式与量程表
- 落槽：选最小 `L` 使 `deadline < currentTime_L + interval_L`，槽号 `(deadline / tick_L) mod S`。
- 无纪元混叠：低层拒绝保证 `deadline >= currentTime_{L-1} + interval_{L-1} >= currentTime_L + tick_L`（严格在游标前），且 `deadline < currentTime_L + interval_L`（一周之内），同槽不会共存两圈元素。
- 量程表（默认 `S=64, K=4`）：

| 层 | 槽宽 tick | 量程 interval |
|----|-----------|---------------|
| 0  | 1         | 64            |
| 1  | 64        | 4 096         |
| 2  | 4 096     | 262 144       |
| 3  | 262 144   | 16 777 216    |

- 默认 `MaxDelay = S^K − S^(K−1) = 64^4 − 64^3 = 16 515 072`：对齐最坏情形（`currentTime_{K-1}` 落后 `now` 至多 `S^(K−1)−1`）下仍保证能放入顶层。

## 三、四处推导
### 1. 到期判定用 `>=`，延迟 0 在下一次 Advance 触发
不变量 1 要求累计推进量「首次达到 d 即触发」。若用 `>`（`now > deadline` 才触发），累计恰为 d 时不触发，直接违反「达到即触发」；故判定为 `now >= deadline`。延迟 0：`deadline = 注册时 now`，注册瞬间已满足 `>=`；但状态只能在 `Advance` 内改变，且 `Advance(0)` 是参数错误，因此它在下一次 `Advance` 的首个 tick 被触发——这是「首次达到或超过」能被观察到的最早时机，两者自洽。

### 2. 同 tick 顺序 = Add 先后
约束：(a) 同一输入序列重复执行结果逐位相同；(b) 顺序不得因某定时器恰好落在高层轮而改变。层级与槽位由「注册时刻与 deadline 之差」决定，是实现细节：同一 deadline 的两个定时器若注册时刻不同会落在不同层，任何基于层/槽/取槽顺序的规则都会被内部结构扰动，违反 (b)。输入中唯一外部可见的全序是 `Add` 先后，故每个定时器携带单调序号 `seq`，每次到期批在触发前按 `seq` 排序——同 tick 内严格按 Add 先后，与层级、槽位无关，且天然满足 (a)。

### 3. 降级时机：推进到槽时惰性降级（flush-on-arrival）
选择：仅当 `now` 越过 `S^L` 的整数倍、游标落到高层某槽时，才把该槽元素按剩余量重新落槽（剩余为 0 则触发）。不选提前降级：提前降级要么每 tick 扫描高层元素（触碰数随 N 增长，违反第四节复杂度约束），要么需要额外维护最早到期索引，徒增复杂度而不改变可观察行为。论证满足不变量 2：惰性降级是 `now` 的纯函数（只在 `now ≡ 0 mod S^L` 发生），而 `Advance` 内部逐 tick 推进，`Advance(100)` 与 100 次 `Advance(1)` 经过完全相同的 `now` 序列、在相同的点做相同的降级，故触发序列逐位相同。

### 4. `Reset` 从当前时刻起算
选择：`deadline = now + delay`（`now` 为 `Reset` 调用时刻）。不变量 1 是对「设定延迟的那个操作」而言的：若从原注册时刻起算，`deadline = 原注册时刻 + d` 可能 `<= now`，`Reset` 后立即触发——相对于这次 `Reset` 的延迟 d，累计推进 0 < d 却触发，违反不早触发。故 `Add` 与 `Reset` 都以各自操作时刻为原点建立新期限，不变量 1 对两者分别成立。

## 其他关键决策
- 待触发窗口可取消：到期批先收集（不置状态），逐条在锁内复查 `state==Pending && gen 未变` 才置 `Fired` 并回调；`Reset` 递增 `gen` 使已收集项失效，`Cancel` 置 `Cancelled` 同理被跳过。回调在不持锁时执行。
- 句柄结果互异：`Cancel`/`Reset` 按状态返回 `ErrTimerCancelled`/`ErrTimerFired`/`ErrTimerNotFound`；已终结句柄保留墓碑以区分「未知」。
- 复杂度计数器 `touchedSlots/touchedTimers` 为非导出字段，每次 `Advance` 清零重计；`Advance(1)` 且元素都在高层时只触碰层 0 一个槽。
- 上限 `MaxAdvance/MaxTimers/MaxDelay` 均可配置，超限立即返回哨兵错误且不改任何状态，调度器不进入终态。
- 并发：`advMu` 串行化 `Advance`，`mu` 保护全部状态；锁序固定 `advMu` 先于 `mu`。
