# futex 等待队列子系统

`futex` 包实现带值校验、优先级队列、位掩码唤醒、重排队与复合唤醒的
futex 风格等待队列。所有操作与查询均可并发调用，结果等价于某个串行
顺序；相同操作序列重放得到完全相同的返回值、队列与状态。

## 构造与容量

`New(w)` 接受同时等待的线程数上限 `w`（1 到 10^6），越界返回
`ErrInvalidParam`，整体拒绝。内存字以非负 int64 地址为键、int32 为
值，缺省为 0；注入时钟 `now` 初值为 0。

## 队列次序与插入规则

- 每个地址一条等待队列，按 **(prio 升序，同 prio 先进先出)** 排序，
  prio 取 0..99，数值小者靠前。
- 每次成功 `Wait` 把等待者**追加到该地址其 prio 组的末尾**，并分配
  全局递增序号 `seq`（从 1 起）。
- `Requeue` 的转移者同样插入目标地址其 prio 组的**末尾**（不按原
  seq 重排），保留 bitset、deadline 与 seq，状态仍为等待。
- 被唤醒序列的次序等于它们离开队列前的队列次序。

## 值校验的原子性

`Wait` 的值校验与入队是一个原子步骤（同一把互斥锁内完成）：不会
出现 `Wait` 读到旧值后、入队前被 `Store`+`Wake` 插队从而丢失唤醒。
`Requeue` 的全部步骤（值校验、唤醒、转移）与 `WakeOp` 的全部步骤
（读旧值、改写、两段唤醒）各自也是一个原子步骤。

`Wait` 的拒绝顺序：**参数非法 → 忙 → 值已变 → 立即超时 → 队列已满**。
任何被拒绝的操作都不改变内存字、队列、线程状态、seq 与时钟。

## bitset 与重排队语义

- `Wake(addr, n, bitset)` 自队首向队尾扫描，唤醒 bitset 按位与非零
  者，至多 n 个；不匹配者**留在原位**；`n=0` 不唤醒任何线程。
- `Requeue(a1, a2, nWake, nRequeue, check, expected)`：`check` 为真
  时先要求 `Load(a1) == expected`，否则报 `ErrValueChanged` 且一个
  线程也不动；然后先唤醒 a1 队首 nWake 个（**不看 bitset**），再把
  此后队首至多 nRequeue 个按原次序转移到 a2。`a1 == a2` 报参数非法。

## WakeOp 的旧值比较

`WakeOp(a1, a2, n1, n2, op, oparg, cmp, cmparg)` 原子地：

1. 读 a2 旧值 `old`，把内存字改写为 `op(old, oparg)`
   （SET/ADD/OR/ANDN/XOR；ADD 按 int32 回绕，ANDN 为 `old &^ oparg`）；
2. 无条件唤醒 a1 前 n1 个等待者；
3. 当 `cmp(old, cmparg)`（EQ/NE/LT/LE/GT/GE）成立时唤醒 a2 前 n2 个。

比较用的是**改写前的旧值**；cmp 不成立时内存字仍被改写。`a1 == a2`
时第二段在已唤醒 n1 个之后的剩余队列上继续，不会重复唤醒。

## 超时次序

`Advance(now)`：`now < 0` 报参数非法，`now` 小于当前时钟报时间回退。
推进后所有 deadline 非零且不大于 now 的等待者出队并置为已超时，按
**(deadline 升序，seq 升序)** 返回。`Wait` 时 deadline 非零且不大于
now 报立即超时（不入队）；deadline 为 0 表示不超时。

## 复杂度计数器

非导出计数器 `examined`（经 `Examined()`/`ResetExamined()` 访问）验证
性能约束：

- `Wake` 考察数 = 被唤醒数 + 被跳过的不匹配者数，与其他地址上的
  等待者数无关（每地址独立队列，按 100 个 prio 分组）。
- `Requeue` 至多考察 nWake+nRequeue 个。
- `Advance` 借助 (deadline, seq) 最小堆，等待者离队在堆中即时删除，
  考察数不超过本次超时个数加一（10^5 规模测试验证）。

## 不变式

任何线程至多出现在一个队列中；各地址队列长度之和等于等待状态线程
数且不超过 W；成功的 Wait 次数恒等于被唤醒数、超时数、中断数与仍
在等待数之和。测试中的 `checkInvariants` 在随机与并发场景后校验。

## 本地验证

```bash
# 全量测试（确定性用例 + 2000 组随机对照 + 并发/性能）
go test ./futex/

# 竞态检测 + 详细日志（随机对照打印输入、输出与判定依据）
go test -race -v ./futex/

# 只跑随机对照或并发无丢失唤醒
go test -run TestRandomAgainstNaive -v ./futex/
go test -race -run TestConcurrentWaitStoreWakeNoLostWakeup ./futex/
```
